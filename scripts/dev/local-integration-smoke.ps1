param(
    [string]$ApiRoot = "http://127.0.0.1:18081",
    [string]$OutputDir = ".tmp\local-integration-smoke",
    [switch]$Live,
    [switch]$SkipFrontend
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location -LiteralPath $repoRoot

if (-not [System.IO.Path]::IsPathRooted($OutputDir)) {
    $OutputDir = Join-Path $repoRoot $OutputDir
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$results = @()

function Invoke-Check {
    param([string]$Name, [scriptblock]$Action)
    $log = Join-Path $OutputDir (($Name.ToLowerInvariant() -replace "[^a-z0-9]+", "-").Trim("-") + ".log")
    Write-Host "==> $Name"
    try {
        $global:LASTEXITCODE = 0
        & $Action *>&1 | Tee-Object -FilePath $log
        if ($LASTEXITCODE -ne 0) { throw "$Name failed with exit code $LASTEXITCODE" }
        $script:results += [pscustomobject]@{ name = $Name; status = "ok"; log = $log }
    }
    catch {
        $_ | Out-String | Tee-Object -FilePath $log -Append
        $script:results += [pscustomobject]@{ name = $Name; status = "fail"; log = $log }
        throw
    }
}

Invoke-Check "Backend tests" { go -C backend test ./... -count=1 }
Invoke-Check "Backend vet" { go -C backend vet ./... }
Invoke-Check "Backend build" { go -C backend build ./... }
Invoke-Check "Sandbox tests" { go -C sandbox test ./... -count=1 }
Invoke-Check "Sandbox vet" { go -C sandbox vet ./... }
Invoke-Check "Sandbox build" { go -C sandbox build ./... }

if (-not $SkipFrontend) {
    Invoke-Check "Frontend install" { npm --prefix frontend ci }
    Invoke-Check "Frontend lint" { npm --prefix frontend run lint }
    Invoke-Check "Frontend build" { npm --prefix frontend run build }
}

Invoke-Check "Compose 16x2 rollout modes" {
    $previousJobs = $env:ALGOFORGE_CUSTOM_GENERATION_API_MODE
    $previousExport = $env:ALGOFORGE_QG15_EXPORT_MODE
    $previousQuality = $env:ALGOFORGE_QUALITY_MODE
    $previousDiversity = $env:ALGOFORGE_S5_DIVERSITY_MODE
    try {
        $combinationCount = 0
        foreach ($jobs in @("jobs-v1", "legacy-only")) {
            foreach ($export in @("qg15-v1", "legacy-only")) {
                foreach ($quality in @("quality-v1", "legacy-only")) {
                    foreach ($diversity in @("diversity-v1", "legacy-only")) {
                        $env:ALGOFORGE_CUSTOM_GENERATION_API_MODE = $jobs
                        $env:ALGOFORGE_QG15_EXPORT_MODE = $export
                        $env:ALGOFORGE_QUALITY_MODE = $quality
                        $env:ALGOFORGE_S5_DIVERSITY_MODE = $diversity
                        $modeLabel = "$jobs/$export/$quality/$diversity"

                        docker compose -f docker-compose.yml config --quiet
                        if ($LASTEXITCODE -ne 0) { throw "Base Compose config failed for $modeLabel" }
                        docker compose -f docker-compose.yml -f docker-compose.local.yml config --quiet
                        if ($LASTEXITCODE -ne 0) { throw "Local Compose config failed for $modeLabel" }
                        $combinationCount++
                    }
                }
            }
        }
        if ($combinationCount -ne 16) { throw "Expected 16 rollout combinations, validated $combinationCount" }

        $modeNames = @(
            "ALGOFORGE_CUSTOM_GENERATION_API_MODE",
            "ALGOFORGE_QG15_EXPORT_MODE",
            "ALGOFORGE_QUALITY_MODE",
            "ALGOFORGE_S5_DIVERSITY_MODE"
        )
        foreach ($modeName in $modeNames) {
            Set-Item -Path "Env:$modeName" -Value ""
        }
        $composeVariants = @(
            @{ Label = "base"; Arguments = @("-f", "docker-compose.yml") },
            @{ Label = "local"; Arguments = @("-f", "docker-compose.yml", "-f", "docker-compose.local.yml") }
        )
        foreach ($variant in $composeVariants) {
            $composeArguments = @("compose") + $variant.Arguments + @("config", "--format", "json")
            $configJSON = & docker @composeArguments
            if ($LASTEXITCODE -ne 0) { throw "$($variant.Label) explicit-empty Compose config failed" }
            $config = $configJSON | ConvertFrom-Json
            foreach ($serviceName in @("api", "worker")) {
                $environment = $config.services.$serviceName.environment
                foreach ($modeName in $modeNames) {
                    $value = $environment.$modeName
                    if ($null -eq $value -or [string]$value -ne "") {
                        throw "$($variant.Label) Compose defaulted explicit-empty $serviceName.$modeName to '$value'"
                    }
                }
            }
        }
    }
    finally {
        $env:ALGOFORGE_CUSTOM_GENERATION_API_MODE = $previousJobs
        $env:ALGOFORGE_QG15_EXPORT_MODE = $previousExport
        $env:ALGOFORGE_QUALITY_MODE = $previousQuality
        $env:ALGOFORGE_S5_DIVERSITY_MODE = $previousDiversity
    }
}

Invoke-Check "Source manifest" {
    & (Join-Path $repoRoot "scripts\release\verify-source-manifest.ps1")
}

Invoke-Check "Release contract" {
    & (Join-Path $repoRoot "scripts\release\verify-release-contract.ps1")
}

Invoke-Check "Hydro chain dry run" {
    & (Join-Path $PSScriptRoot "hydro-chain-smoke.ps1") -ApiBaseUrl ($ApiRoot.TrimEnd("/") + "/api/v1") -PlanOnly -ProblemId "550e8400-e29b-41d4-a716-446655440000"
}

if ($Live) {
    Invoke-Check "Live API health" {
        $health = Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 -Uri ($ApiRoot.TrimEnd("/") + "/health")
        if ($health.StatusCode -ne 200) { throw "Unexpected health status $($health.StatusCode)" }
    }
    Invoke-Check "Live integration capabilities" {
        $capabilities = Invoke-RestMethod -TimeoutSec 10 -Uri ($ApiRoot.TrimEnd("/") + "/api/v1/integration/capabilities")
        if ($capabilities.schema_version -ne "algoforge.integration-capabilities.v1") { throw "Unexpected capabilities schema" }
        if ($capabilities.target_oj.external_import_verified -ne $false) { throw "External OJ verification must remain false until a real import" }
    }
}

$summary = [pscustomobject]@{
    generated_at = (Get-Date).ToString("o")
    live = [bool]$Live
    version = (Get-Content -Raw -LiteralPath VERSION).Trim()
    steps = $results
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $OutputDir "summary.json") -Encoding UTF8
Write-Host "Integration smoke passed. Summary: $(Join-Path $OutputDir 'summary.json')"

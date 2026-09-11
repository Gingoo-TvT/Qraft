param(
    [string]$ApiBaseUrl = "http://127.0.0.1:18081",
    [string]$StatementModel = "statement-model-id",
    [string]$StatementBaseUrl = "",
    [string]$StatementProvider = "anthropic-compatible",
    [string]$StatementApiKeyRef = "env:ALGOFORGE_STATEMENT_LLM_KEY",
    [string]$VerificationModel = "review-model-id",
    [string]$VerificationBaseUrl = "",
    [string]$VerificationProvider = "anthropic-compatible",
    [string]$VerificationApiKeyRef = "env:ALGOFORGE_REVIEW_LLM_KEY",
    [string]$Token = "",
    [switch]$Invoke
)

$ErrorActionPreference = "Stop"

function New-RuntimeConfig {
    param(
        [string]$Model,
        [string]$BaseUrl,
        [string]$Provider,
        [string]$ApiKeyRef
    )

    $config = [ordered]@{}
    if ($Model.Trim()) { $config.model = $Model.Trim() }
    if ($BaseUrl.Trim()) { $config.base_url = $BaseUrl.Trim() }
    if ($Provider.Trim()) { $config.provider = $Provider.Trim() }
    if ($ApiKeyRef.Trim()) { $config.api_key_ref = $ApiKeyRef.Trim() }
    return $config
}

$payload = [ordered]@{
    level = "algorithm"
    difficulty = 1600
    tags = @("dp", "greedy")
    contest_style = "icpc"
    time_limit = 2000
    memory_limit = 256
    test_data_config = [ordered]@{
        num_test_cases = 20
        num_samples = 2
    }
    require_review = $false
    generate_editorial = $true
    languages = @("cpp")
    similar_limit = 3
    locale = "zh"
    provider_config = [ordered]@{
        statement = New-RuntimeConfig `
            -Model $StatementModel `
            -BaseUrl $StatementBaseUrl `
            -Provider $StatementProvider `
            -ApiKeyRef $StatementApiKeyRef
        verification = New-RuntimeConfig `
            -Model $VerificationModel `
            -BaseUrl $VerificationBaseUrl `
            -Provider $VerificationProvider `
            -ApiKeyRef $VerificationApiKeyRef
    }
}

$json = $payload | ConvertTo-Json -Depth 8

if (-not $Invoke) {
    Write-Output "DRY_RUN generate contract payload:"
    Write-Output $json
    Write-Output "Pass -Invoke to POST this payload to $ApiBaseUrl/api/v1/problems/generate."
    exit 0
}

$headers = @{
    "Content-Type" = "application/json"
}
if ($Token.Trim()) {
    $headers.Authorization = "Bearer $($Token.Trim())"
}

$url = $ApiBaseUrl.TrimEnd("/") + "/api/v1/problems/generate"
Invoke-RestMethod -Method Post -Uri $url -Headers $headers -Body $json

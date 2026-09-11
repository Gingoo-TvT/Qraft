param(
    [string]$ApiBaseUrl = "http://127.0.0.1:18081/api/v1",
    [string]$Token = "",
    [string]$ProblemId = "",
    [switch]$Generate,
    [string]$GeneratePayloadPath = "",
    [string]$OutputDir = ".tmp\hydro-chain-smoke",
    [int]$PollSeconds = 5,
    [int]$MaxPolls = 120,
    [switch]$PlanOnly
)

$ErrorActionPreference = "Stop"

function Get-ApiUrl {
    param([string]$Path)
    return $ApiBaseUrl.TrimEnd("/") + "/" + $Path.TrimStart("/")
}

function Get-AuthHeaders {
    $headers = @{
        Accept = "application/json"
    }
    if ($Token.Trim()) {
        $headers.Authorization = "Bearer $($Token.Trim())"
    }
    return $headers
}

function Invoke-JsonRequest {
    param(
        [ValidateSet("GET", "POST")]
        [string]$Method,
        [string]$Path,
        [object]$Body = $null
    )

    $headers = Get-AuthHeaders
    $uri = Get-ApiUrl $Path
    if ($null -ne $Body) {
        $headers["Content-Type"] = "application/json"
        $json = $Body | ConvertTo-Json -Depth 12
        return Invoke-RestMethod -Method $Method -Uri $uri -Headers $headers -Body $json
    }
    return Invoke-RestMethod -Method $Method -Uri $uri -Headers $headers
}

function Wait-Workflow {
    param([string]$WorkflowId)

    $terminal = @("Completed", "Failed", "Canceled", "Terminated", "TimedOut")
    for ($i = 0; $i -lt $MaxPolls; $i++) {
        $response = Invoke-JsonRequest -Method GET -Path "workflows/$WorkflowId"
        $data = $response.data
        $status = [string]$data.execution_status
        if (-not $status) {
            $status = [string]$data.status
        }
        Write-Output "workflow=$WorkflowId status=$status"
        if ($terminal -contains $status) {
            return $response
        }
        Start-Sleep -Seconds $PollSeconds
    }
    throw "workflow $WorkflowId did not finish after $MaxPolls polls"
}

function Get-ProblemIdFromWorkflow {
    param([object]$WorkflowResponse)

    $problemId = $WorkflowResponse.data.state.problem_id
    if (-not $problemId) {
        throw "workflow response does not contain data.state.problem_id"
    }
    return [string]$problemId
}

function Save-HydroPackage {
    param(
        [string]$Id,
        [string]$Destination
    )

    $headers = Get-AuthHeaders
    $uri = Get-ApiUrl "problems/$Id/hydro.zip"
    Invoke-WebRequest -Method GET -Uri $uri -Headers $headers -OutFile $Destination
    return $Destination
}

function Invoke-HydroPreflight {
    param([string]$ZipPath)

    Add-Type -AssemblyName System.Net.Http
    $client = [System.Net.Http.HttpClient]::new()
    try {
        if ($Token.Trim()) {
            $client.DefaultRequestHeaders.Authorization =
                [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $Token.Trim())
        }
        $content = [System.Net.Http.MultipartFormDataContent]::new()
        $resolvedZip = (Resolve-Path -LiteralPath $ZipPath).Path
        $stream = [System.IO.File]::OpenRead($resolvedZip)
        try {
            $fileContent = [System.Net.Http.StreamContent]::new($stream)
            $fileContent.Headers.ContentType =
                [System.Net.Http.Headers.MediaTypeHeaderValue]::Parse("application/zip")
            $content.Add($fileContent, "file", [System.IO.Path]::GetFileName($ZipPath))
            $response = $client.PostAsync((Get-ApiUrl "problems/hydro/validate"), $content).GetAwaiter().GetResult()
            $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            if (-not $response.IsSuccessStatusCode) {
                throw "Hydro preflight failed with HTTP $([int]$response.StatusCode): $body"
            }
            return $body | ConvertFrom-Json
        }
        finally {
            $stream.Dispose()
            $content.Dispose()
        }
    }
    finally {
        $client.Dispose()
    }
}

if ($PlanOnly) {
    Write-Output "Hydro integration chain plan:"
    $step = 1
    if ($Generate) {
        Write-Output "$step. POST /problems/generate using GeneratePayloadPath=$GeneratePayloadPath"
        $step++
        Write-Output "$step. Poll GET /workflows/:workflow_id until completed and read data.state.problem_id"
        $step++
    }
    else {
        Write-Output "$step. Use existing ProblemId=$ProblemId"
        $step++
    }
    Write-Output "$step. POST /problems/:problem_id/validate"
    $step++
    Write-Output "$step. Poll validation workflow until terminal"
    $step++
    Write-Output "$step. GET /problems/:problem_id/hydro.zip"
    $step++
    Write-Output "$step. POST /problems/hydro/validate with multipart field 'file'"
    exit 0
}

if (-not $ProblemId.Trim() -and -not $Generate) {
    throw "Provide -ProblemId for an existing problem, or pass -Generate with -GeneratePayloadPath."
}

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

if ($Generate) {
    if (-not $GeneratePayloadPath.Trim()) {
        throw "-Generate requires -GeneratePayloadPath"
    }
    $payload = Get-Content -LiteralPath $GeneratePayloadPath -Raw | ConvertFrom-Json
    $generated = Invoke-JsonRequest -Method POST -Path "problems/generate" -Body $payload
    $workflowId = $generated.data.workflow_id
    if (-not $workflowId) {
        throw "generation response does not contain data.workflow_id"
    }
    $generationWorkflow = Wait-Workflow -WorkflowId $workflowId
    $ProblemId = Get-ProblemIdFromWorkflow -WorkflowResponse $generationWorkflow
}

Write-Output "problem_id=$ProblemId"

$validation = Invoke-JsonRequest -Method POST -Path "problems/$ProblemId/validate"
$validationWorkflowId = $validation.data.workflow_id
if ($validationWorkflowId) {
    [void](Wait-Workflow -WorkflowId $validationWorkflowId)
}

$zipPath = Join-Path $OutputDir "$ProblemId-hydro.zip"
[void](Save-HydroPackage -Id $ProblemId -Destination $zipPath)
Write-Output "hydro_zip=$zipPath"

$preflight = Invoke-HydroPreflight -ZipPath $zipPath
$reportPath = Join-Path $OutputDir "$ProblemId-hydro-preflight.json"
$preflight | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $reportPath -Encoding UTF8
Write-Output "preflight_report=$reportPath"

if (-not $preflight.data.valid) {
    throw "Hydro preflight returned valid=false"
}

Write-Output "Hydro integration chain completed."

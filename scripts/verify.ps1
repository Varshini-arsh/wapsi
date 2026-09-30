$ErrorActionPreference = "Stop"

function Get-Json($url, $method = "Get", $body = $null) {
  $params = @{ Uri = $url; Method = $method; ContentType = "application/json" }
  if ($null -ne $body) { $params.Body = ($body | ConvertTo-Json -Compress) }
  Invoke-RestMethod @params
}

foreach ($port in 4000, 4001) {
  $base = "http://localhost:$port"
  $health = Get-Json "$base/api/health"
  if (-not $health.ok) { throw "Gateway $port is unhealthy" }
  Write-Host "Gateway ${port}: $($health.mode) OK"
  foreach ($rule in "OWN_FIRST", "PRORATA", "FIFO", "LIBR") {
    $trace = Get-Json "$base/api/cases/C001/trace" "Post" @{ caseId = "C001"; rule = $rule }
    $sum = $trace.totalRecoverable + $trace.totalCashedOut
    if ($sum -ne 8000000) { throw "Gateway $port rule $rule failed conservation: $sum" }
    Write-Host "${port} ${rule}: $($trace.totalRecoverable) / $($trace.totalCashedOut)"
  }
}

$uiHealth = Get-Json "http://localhost:5173/api/health"
Write-Host "UI proxy: $($uiHealth.mode)"
Write-Host "Verification passed"

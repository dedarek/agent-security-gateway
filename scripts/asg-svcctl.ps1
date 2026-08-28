# ASG service control — runs as SYSTEM via the ASG-ServiceControl scheduled task.
#
# Scope is deliberately narrow: it stops, cleans and restarts ONLY the five ASG
# services. It accepts no arguments, so triggering the task can never be turned
# into arbitrary code execution.
#
# Ordering matters: ASG-KGWorker must be fully listening on :8902 BEFORE the
# gateway starts, otherwise the gateway seeds an empty graph. The worker
# downloads fastembed models on a cold start, hence the generous wait.

$ErrorActionPreference = 'Continue'
$log = 'D:\proj\agent-security-gateway\logs\svcctl.log'
New-Item -ItemType Directory -Force -Path (Split-Path $log) | Out-Null

function Log($m) {
  $line = ('{0} {1}' -f (Get-Date -Format 'HH:mm:ss'), $m)
  Add-Content -Path $log -Value $line -Encoding utf8
}

Set-Content -Path $log -Value ('=== svcctl run {0} ===' -f (Get-Date)) -Encoding utf8

$svcs  = 'ASG-Gateway','ASG-KGWorker','ASG-Behavior','ASG-OutputGuard','ASG-Cpolar'
$ports = 8080,8090,8901,8902,8903

# --- stop everything -------------------------------------------------------
# Stop Pending wrappers ignore Stop-Service / sc stop, and both can hang
# indefinitely when the wrapper is stuck (observed: OutputGuard then Gateway).
# Wrap each with a hard timeout so one hung service never blocks the whole task.
foreach ($s in $svcs) {
  Log "stopping $s"
  $svcName = $s
  # Stop-Service can hang on Stop Pending — run in a job with 5s timeout
  $j1 = Start-Job -ScriptBlock { try { Stop-Service $using:svcName -Force -EA Stop 2>&1 | Out-String } catch { $_.Exception.Message | Out-String } }
  if (Wait-Job $j1 -Timeout 5) { $o1 = Receive-Job $j1; foreach ($l in ($o1 -split "`r?`n")) { if ($l.Trim() -ne '') { Log "  Stop-Service $svcName : $l" } }; Remove-Job $j1 -Force -EA SilentlyContinue } else { Stop-Job $j1 -EA SilentlyContinue; Remove-Job $j1 -Force -EA SilentlyContinue; Log "  Stop-Service $svcName : TIMEOUT after 5s" }
  $j2 = Start-Job -ScriptBlock { & sc.exe stop $using:svcName 2>&1 | Out-String }
  if (Wait-Job $j2 -Timeout 5) { $o2 = Receive-Job $j2; foreach ($l in ($o2 -split "`r?`n")) { if ($l.Trim() -ne '') { Log "  sc stop $svcName : $l" } }; Remove-Job $j2 -Force -EA SilentlyContinue } else { Stop-Job $j2 -EA SilentlyContinue; Remove-Job $j2 -Force -EA SilentlyContinue; Log "  sc stop $svcName : TIMEOUT after 5s" }
}
Start-Sleep -Seconds 2

# nssm wrappers stuck in Stop Pending survive sc stop. Kill the wrapper PID
# hard, using taskkill /F /T which also kills child python.exe.
foreach ($s in $svcs) {
  $o = Get-CimInstance Win32_Service -Filter "Name='$s'" -EA SilentlyContinue
  if ($o -and $o.ProcessId -gt 0) {
    $before = $o.ProcessId
    Log "killing stale wrapper $s pid=$before"
    & taskkill /F /PID $before /T 2>&1 | ForEach-Object { Log "  taskkill $s : $_" }
    # fallback if taskkill was denied
    try { Stop-Process -Id $before -Force -EA Stop } catch {}
  }
}
Start-Sleep -Seconds 4
# verify none are still Stop Pending; if they are, we log and continue
foreach ($s in $svcs) {
  $q = & sc.exe query $s 2>&1 | Out-String
  if ($q -match 'STOP_PENDING') { Log "WARN $s still STOP_PENDING after kill" }
}

# --- free the ports --------------------------------------------------------
foreach ($i in 1..3) {
  $listeners = Get-NetTCPConnection -LocalPort $ports -State Listen -EA SilentlyContinue
  if (-not $listeners) { break }
  foreach ($l in $listeners) { Stop-Process -Id $l.OwningProcess -Force -EA SilentlyContinue }
  Start-Sleep -Seconds 2
}
$left = (Get-NetTCPConnection -LocalPort $ports -State Listen -EA SilentlyContinue | Measure-Object).Count
Log "ports still listening after cleanup: $left"

# --- start in dependency order --------------------------------------------
foreach ($s in 'ASG-Behavior','ASG-OutputGuard','ASG-KGWorker') {
  & sc.exe start $s | Out-Null
  Log "started $s"
}
# wait for the KG worker to actually answer, not just for the service to exist
$ok = $false
foreach ($i in 1..40) {
  Start-Sleep -Seconds 2
  try {
    $r = Invoke-WebRequest 'http://127.0.0.1:8902/health' -TimeoutSec 3 -UseBasicParsing
    if ($r.StatusCode -eq 200) { $ok = $true; Log "kgworker healthy after $($i*2)s: $($r.Content)"; break }
  } catch {}
}
if (-not $ok) { Log 'kgworker did NOT become healthy within 80s' }

& sc.exe start ASG-Gateway | Out-Null
Log 'started ASG-Gateway'
& sc.exe start ASG-Cpolar | Out-Null

# --- verify ----------------------------------------------------------------
$gw = $false
foreach ($i in 1..20) {
  Start-Sleep -Seconds 2
  try {
    $r = Invoke-WebRequest 'http://127.0.0.1:8090/healthz' -TimeoutSec 3 -UseBasicParsing
    if ($r.StatusCode -eq 200) { $gw = $true; Log "gateway healthy after $($i*2)s"; break }
  } catch {}
}
if (-not $gw) { Log 'gateway did NOT become healthy within 40s' }

Get-NetTCPConnection -LocalPort $ports -State Listen -EA SilentlyContinue |
  Select-Object LocalPort,OwningProcess | Sort-Object LocalPort |
  ForEach-Object { Log ("listen {0} pid={1}" -f $_.LocalPort, $_.OwningProcess) }

Log 'done'

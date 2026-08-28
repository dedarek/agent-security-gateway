# ASG service control task — one-time registration. RUN AS ADMINISTRATOR.
#
# Design notes (learned the hard way):
#   * A task registered under the SYSTEM principal cannot be queried or started
#     by a standard user. Task Scheduler enforces this through the task's own
#     security descriptor in the registry (TaskCache\Tasks\{GUID}\SD) — granting
#     the user rights on %SystemRoot%\System32\Tasks\<name> does NOT help.
#   * Registering the task under the invoking USER with RunLevel=Highest gives
#     the owner the right to trigger it, and the task still runs elevated with
#     no UAC prompt. That is what we want.
#
# Mode A — convenience first (chosen 2026-08-29): the task runs
# D:\proj\agent-security-gateway\scripts\asg-svcctl.ps1 directly.
# No copy to C:\ProgramData, no icacls lock. Future edits to that file
# take effect on the next trigger without another admin step.
# Trade-off: whoever can write that file can run code as Highest.

$ErrorActionPreference = 'Stop'

$srcScript = 'D:\proj\agent-security-gateway\scripts\asg-svcctl.ps1'

if (-not (Test-Path $srcScript)) { throw "missing $srcScript" }

$taskName = 'ASG-ServiceControl'
$me       = "$env:USERDOMAIN\$env:USERNAME"

Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -EA SilentlyContinue

$action    = New-ScheduledTaskAction -Execute 'powershell.exe' `
             -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$srcScript`""
$principal = New-ScheduledTaskPrincipal -UserId $me -LogonType Interactive -RunLevel Highest
$settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries `
             -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Minutes 10)

Register-ScheduledTask -TaskName $taskName -Action $action `
  -Principal $principal -Settings $settings -Force | Out-Null

Write-Host "registered: $taskName"
Write-Host "  runs as : $me (Highest, no UAC prompt)"
Write-Host "  executes: $srcScript (user-writable, instant effect)"
Write-Host "verify with: schtasks /query /tn `"$taskName`""

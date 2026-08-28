#Requires -RunAsAdministrator
# ASG Services — uninstall
# Run: powershell -ExecutionPolicy Bypass -File scripts/uninstall-services.ps1

$services = @("ASG-Gateway","ASG-Cpolar","ASG-KGWorker","ASG-Connect")
function Test-Nssm { try { Get-Command nssm -ErrorAction Stop | Out-Null; return $true } catch { return $false } }
$hasNssm = Test-Nssm

foreach ($name in $services) {
  Write-Host "[ASG] Removing $name ..."
  try {
    if ($hasNssm) { nssm stop $name 2>$null; Start-Sleep 1; nssm remove $name confirm 2>$null }
    else { sc.exe stop $name 2>$null | Out-Null; Start-Sleep 1; sc.exe delete $name 2>$null | Out-Null }
    Write-Host "  removed"
  } catch { Write-Host "  $_" -ForegroundColor Yellow }
}
Write-Host "[ASG] Done."

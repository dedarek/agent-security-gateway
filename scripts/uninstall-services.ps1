#Requires -RunAsAdministrator
# ASG Services — uninstall (5 services)
# Run: powershell -ExecutionPolicy Bypass -File scripts/uninstall-services.ps1

$services = @("ASG-Gateway","ASG-Cpolar","ASG-Behavior","ASG-KGWorker","ASG-OutputGuard")
$extraPaths = @("C:/Users/$env:USERNAME/AppData/Local/Microsoft/WinGet/Links", "D:/tools/bin")
foreach ($p in $extraPaths) { if ((Test-Path $p) -and ($env:Path -notlike "*$p*")) { $env:Path += ";$p" } }
$wingetNssm = "C:/Users/$env:USERNAME/AppData/Local/Microsoft/WinGet/Packages/NSSM.NSSM_Microsoft.Winget.Source_8wekyb3d8bbwe/nssm-2.24-101-g897c7ad/win64/nssm.exe"
if ((Test-Path $wingetNssm) -and !(Get-Command nssm -ErrorAction SilentlyContinue)) { $env:Path += ";$(Split-Path $wingetNssm)" }
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
try {
  $legacy = Get-Service "ASG-Connect" -ErrorAction SilentlyContinue
  if ($legacy) {
    Write-Host "[ASG] Removing legacy ASG-Connect ..."
    if ($hasNssm) { nssm stop ASG-Connect 2>$null; nssm remove ASG-Connect confirm 2>$null } else { sc.exe stop ASG-Connect 2>$null | Out-Null; sc.exe delete ASG-Connect 2>$null | Out-Null }
  }
} catch {}
Write-Host "[ASG] Done."

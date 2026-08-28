#Requires -RunAsAdministrator
# ASG Services — install with nssm (or sc fallback) + auto-restart
# Run: powershell -ExecutionPolicy Bypass -File scripts/install-services.ps1
# Requires: nssm (choco install nssm) or falls back to sc

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $root) { $root = "D:/proj/agent-security-gateway" }
$root = (Resolve-Path $root).Path
Write-Host "[ASG] root=$root"

function Test-Nssm { try { Get-Command nssm -ErrorAction Stop | Out-Null; return $true } catch { return $false } }
$hasNssm = Test-Nssm
if ($hasNssm) { Write-Host "[ASG] nssm found" } else { Write-Host "[ASG] nssm not found — will use sc (no auto-restart tuning). Install via: choco install nssm" -ForegroundColor Yellow }

$services = @(
  @{ Name="ASG-Gateway"; Display="ASG Gateway (8090)"; Bin="bin/gateway.exe"; Args="serve -config deploy/config.dev.yaml"; Dir=$root; Desc="Agent Security Gateway — MCP + LLM + WebUI" },
  @{ Name="ASG-Cpolar"; Display="ASG Cpolar Tunnel"; Bin="cpolar"; Args="start-all"; Dir=$root; Desc="Cpolar public ingress for ASG Gateway"; IsCpolar=$true },
  @{ Name="ASG-KGWorker"; Display="ASG KG Worker (8902)"; Bin="python"; Args="internal/kgbridge/asg_kg_worker.py"; Dir=$root; Desc="Semantica KG worker"; IsPython=$true }
)

foreach ($svc in $services) {
  $name = $svc.Name
  Write-Host "`n[ASG] Installing $name ..."

  # Remove existing
  try { if ($hasNssm) { nssm remove $name confirm 2>$null } else { sc.exe delete $name 2>$null | Out-Null }; Start-Sleep 1 } catch {}

  if ($svc.IsCpolar) {
    $exe = (Get-Command cpolar -ErrorAction SilentlyContinue).Source
    if (-not $exe) { $exe = "C:/Users/$env:USERNAME/AppData/Local/cpolar/cpolar.exe" }
    if (-not (Test-Path $exe)) { Write-Host "  SKIP $name: cpolar not found at $exe" -ForegroundColor Yellow; continue }
    $binPath = "`"$exe`" $($svc.Args)"
    $workDir = $root
  } elseif ($svc.IsPython) {
    $py = "python"
    try { $py = (Get-Command python -ErrorAction Stop).Source } catch {}
    $binPath = "`"$py`" `"$root/$($svc.Args)`""
    $workDir = $root
  } else {
    $exe = Join-Path $root $svc.Bin
    if (-not (Test-Path $exe)) { Write-Host "  SKIP $name: $exe not found (build first: go build -o bin/gateway.exe ./cmd/gateway)" -ForegroundColor Yellow; continue }
    $binPath = "`"$exe`" $($svc.Args)"
    $workDir = $root
  }

  if ($hasNssm) {
    nssm install $name $binPath
    nssm set $name AppDirectory $workDir
    nssm set $name DisplayName $svc.Display
    nssm set $name Description $svc.Desc
    nssm set $name AppExit Default Restart
    nssm set $name AppRestartDelay 5000
    nssm set $name AppStdout "$root/logs/$name.log"
    nssm set $name AppStderr "$root/logs/$name.log"
    nssm set $name AppRotateFiles 1
    nssm set $name AppRotateOnline 1
    nssm set $name AppRotateBytes 10485760
    nssm set $name Start SERVICE_AUTO_START
    nssm set $name DependOnService ""
    Write-Host "  nssm installed: $name"
    try { nssm start $name; Write-Host "  started" } catch { Write-Host "  start failed: $_" -ForegroundColor Yellow }
  } else {
    # sc fallback — simple service without nssm features
    $bin = $binPath -replace '"',''
    sc.exe create $name binPath= $bin start= auto DisplayName= $svc.Display 2>&1 | Out-Null
    sc.exe description $name $svc.Desc 2>&1 | Out-Null
    sc.exe start $name 2>&1 | Out-Null
    Write-Host "  sc installed: $name (limited restart)"
  }
}

Write-Host "`n[ASG] Done. Check: Get-Service ASG-* | Format-Table"
Write-Host "Logs: $root/logs/"
Write-Host "Uninstall: powershell -File scripts/uninstall-services.ps1"

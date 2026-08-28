#Requires -RunAsAdministrator
# ASG Services — install with nssm (or sc fallback) + auto-restart (5 services)
# Run: powershell -ExecutionPolicy Bypass -File scripts/install-services.ps1
# Requires: nssm (winget install NSSM.NSSM) — sc fallback has limited restart.

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $root) { $root = "D:/proj/agent-security-gateway" }
$root = (Resolve-Path $root).Path
Write-Host "[ASG] root=$root"

# Ensure nssm is on PATH (winget Links + D:/tools/bin)
$extraPaths = @("C:/Users/$env:USERNAME/AppData/Local/Microsoft/WinGet/Links", "D:/tools/bin")
foreach ($p in $extraPaths) { if ((Test-Path $p) -and ($env:Path -notlike "*$p*")) { $env:Path += ";$p" } }
$wingetNssm = "C:/Users/$env:USERNAME/AppData/Local/Microsoft/WinGet/Packages/NSSM.NSSM_Microsoft.Winget.Source_8wekyb3d8bbwe/nssm-2.24-101-g897c7ad/win64/nssm.exe"
if ((Test-Path $wingetNssm) -and !(Get-Command nssm -ErrorAction SilentlyContinue)) { $env:Path += ";$(Split-Path $wingetNssm)" }

function Test-Nssm { try { Get-Command nssm -ErrorAction Stop | Out-Null; return $true } catch { return $false } }
$hasNssm = Test-Nssm
if ($hasNssm) { Write-Host "[ASG] nssm found: $(Get-Command nssm | Select-Object -ExpandProperty Source)" } else { Write-Host "[ASG] nssm not found — will use sc (no auto-restart tuning). Install: winget install --id NSSM.NSSM --accept-source-agreements --accept-package-agreements" -ForegroundColor Yellow }

$logDir = Join-Path $root "logs"
if (!(Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir | Out-Null }

$uvPy = "C:/Users/$env:USERNAME/AppData/Roaming/uv/python/cpython-3.11-windows-x86_64-none/python.exe"
if (!(Test-Path $uvPy)) { $uvPy = (Get-Command python -ErrorAction SilentlyContinue).Source }
if (!(Test-Path $uvPy)) { $uvPy = "python" }
$hermesSite = "C:/Users/$env:USERNAME/AppData/Local/hermes/hermes-agent/venv/Lib/site-packages"
$gatewayExe = Join-Path $root "bin/gateway.exe"
$cpolarExe = "D:/cpolar/cpolar.exe"
if (!(Test-Path $cpolarExe)) {
  $cpolarExe = (Get-Command cpolar -ErrorAction SilentlyContinue).Source
  if (-not $cpolarExe) { $cpolarExe = "C:/Users/$env:USERNAME/AppData/Local/cpolar/cpolar.exe" }
}
$kgToken = $env:ASG_KG_WORKER_TOKEN
if (-not $kgToken) { $kgToken = "asg-1787764330257444600" }

$services = @(
  @{ Name="ASG-Gateway"; Display="ASG Gateway (8090)"; Desc="Agent Security Gateway — MCP + LLM + WebUI"; Exe=$gatewayExe; Args="serve -config deploy/config.dev.yaml"; Dir=$root; Env=@() },
  @{ Name="ASG-Cpolar"; Display="ASG Cpolar Tunnel"; Desc="Cpolar public ingress for ASG Gateway (subdomain asg-gateway)"; Exe=$cpolarExe; Args="start asg-console --log=stdout"; Dir=$root; Env=@() },
  @{ Name="ASG-Behavior"; Display="ASG Behavior Sidecar (8901)"; Desc="Invariant behavior sidecar — policy.iv (8901)"; Exe=$uvPy; Args="D:/proj/agent-security-gateway/intelligence/analyzer/sidecar.py --policy D:/proj/agent-security-gateway/intelligence/analyzer/policy.iv --port 8901"; Dir="D:/proj/agent-security-gateway/intelligence/analyzer"; Env=@("PYTHONPATH=$hermesSite", "LOCAL_POLICY=1") },
  @{ Name="ASG-KGWorker"; Display="ASG KG Worker (8902)"; Desc="Semantica KG worker (8902)"; Exe=$uvPy; Args="D:/proj/agent-security-gateway/internal/kgbridge/asg_kg_worker.py --port 8902 --semantica-path D:/proj/semantica --worker-token $kgToken"; Dir=$root; Env=@("PYTHONPATH=$hermesSite;D:/proj/semantica") },
  @{ Name="ASG-OutputGuard"; Display="ASG OutputGuard Sidecar (8903)"; Desc="OutputGuard sidecar (8903)"; Exe=$uvPy; Args="D:/proj/agent-security-gateway/intelligence/outputguard/sidecar.py --port 8903"; Dir=$root; Env=@("PYTHONPATH=$hermesSite") }
)

foreach ($svc in $services) {
  $name = $svc.Name
  Write-Host "`n[ASG] Installing $name ..."
  $exe = $svc.Exe
  try {
    $existing = Get-Service $name -ErrorAction SilentlyContinue
    if ($existing) {
      Write-Host "  removing existing $name ..."
      if ($hasNssm) { nssm stop $name 2>$null; Start-Sleep 1; nssm remove $name confirm 2>$null }
      else { sc.exe stop $name 2>$null | Out-Null; Start-Sleep 1; sc.exe delete $name 2>$null | Out-Null }
      Start-Sleep 1
    } else { if ($hasNssm) { nssm remove $name confirm 2>$null } }
  } catch {}
  $workDir = $svc.Dir; $exePath = $svc.Exe; $exeArgs = $svc.Args
  if ($hasNssm) {
    nssm install $name "$exePath"
    nssm set $name Application "$exePath"
    nssm set $name AppParameters $exeArgs
    nssm set $name AppDirectory $workDir
    nssm set $name DisplayName $svc.Display
    nssm set $name Description $svc.Desc
    if ($svc.Env -and $svc.Env.Count -gt 0) { $envStr = ($svc.Env -join " "); nssm set $name AppEnvironmentExtra $envStr }
    nssm set $name AppExit Default Restart
    nssm set $name AppRestartDelay 5000
    nssm set $name AppStdout "$logDir/$name.log"
    nssm set $name AppStderr "$logDir/$name.log"
    nssm set $name AppRotateFiles 1
    nssm set $name AppRotateOnline 1
    nssm set $name AppRotateBytes 10485760
    nssm set $name Start SERVICE_AUTO_START
    nssm set $name DependOnService ""
    Write-Host "  nssm installed: $name (AppDirectory=$workDir)"
    try { nssm start $name; Write-Host "  started" } catch { Write-Host "  start failed: $_" -ForegroundColor Yellow }
  } else {
    $bin = "`"$exePath`" $exeArgs"
    sc.exe create $name binPath= $bin start= auto DisplayName= $svc.Display 2>&1 | Out-Null
    sc.exe description $name $svc.Desc 2>&1 | Out-Null
    sc.exe failure $name reset= 60 actions= restart/5000 2>&1 | Out-Null
    sc.exe start $name 2>&1 | Out-Null
    Write-Host "  sc installed: $name (restart 5s)"
  }
}
Write-Host "`n[ASG] Done. Check: Get-Service ASG-* | Format-Table -Auto"
Write-Host "Logs: $logDir"
Write-Host "Uninstall: powershell -ExecutionPolicy Bypass -File scripts/uninstall-services.ps1"

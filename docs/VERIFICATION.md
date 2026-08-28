# ASG Verification — M6 close-the-gap (Task 10-13)

_Generated 2026-08-28 after sidecar dedupe. All commands below are verbatim from `Get-CimInstance Win32_Process` listeners, not from memory._

## Task 10 — Dedupe evidence

### Listeners (Get-NetTCPConnection -State Listen) — final 2026-08-28 17:40
```
PORT 8901 PID=63536 LISTENING
PORT 8902 PID=44896 LISTENING   (restarted after shim-kill; previous PID 12560 also valid)
PORT 8903 PID=18304 LISTENING
PORT 8090 PID=24028 LISTENING
```

### Python sidecar processes (post-cleanup, single uv-python per port)
```
PID=63536  .../python.exe D:/proj/agent-security-gateway/intelligence/analyzer/sidecar.py --policy .../policy.iv --port 8901
PID=44896  .../python.exe D:/proj/agent-security-gateway/internal/kgbridge/asg_kg_worker.py --port 8902 --semantica-path D:/proj/semantica --worker-token asg-1787764330257444600
PID=18304  .../python.exe intelligence/outputguard/sidecar.py --port 8903
```
Previous zombie parents (66524,38776,22768) were hermes-venv shim wrappers (uv shim staying resident + child uv python listening). Killing the shim also terminated the child, so all three sidecars were restarted as single uv-python processes (no shim duplication) with explicit `PYTHONPATH=C:/Users/yyyyc/AppData/Local/hermes/hermes-agent/venv/Lib/site-packages` (and `;D:/proj/semantica` for KG).

### Health (curl — every port exactly 1 listener)
```
GET http://127.0.0.1:8901/health -> 200 {"status":"ok"}
GET http://127.0.0.1:8903/health -> 200 {"status":"ok","guard":false,"scanners":0}
GET http://127.0.0.1:8902/health -> 200 {"status":"ok","pid":44896,"worker_token":"asg-1787764330257444600","graph_ready":true}
GET http://127.0.0.1:8090/healthz -> 200 ok
GET https://asg-gateway.vip.cpolar.cn/healthz -> 200 (cpolar 27308, subdomain asg-gateway, region cn_vip)
```

### Canonical start commands (for Task 12 nssm AppDirectory/AppParameters)

| Service | Port | AppDirectory | Application | AppParameters | Env PYTHONPATH |
|---|---|---|---|---|---|
| ASG-Behavior | 8901 | `D:/proj/agent-security-gateway/intelligence/analyzer` | `C:/Users/yyyyc/AppData/Roaming/uv/python/cpython-3.11-windows-x86_64-none/python.exe` | `D:/proj/agent-security-gateway/intelligence/analyzer/sidecar.py --policy D:/proj/agent-security-gateway/intelligence/analyzer/policy.iv --port 8901` | `C:/Users/yyyyc/AppData/Local/hermes/hermes-agent/venv/Lib/site-packages` |
| ASG-KGWorker | 8902 | `D:/proj/agent-security-gateway` | `C:/Users/yyyyc/AppData/Roaming/uv/python/cpython-3.11-windows-x86_64-none/python.exe` | `D:/proj/agent-security-gateway/internal/kgbridge/asg_kg_worker.py --port 8902 --semantica-path D:/proj/semantica --worker-token <from env ASG_KG_WORKER_TOKEN or asg-1787764330257444600>` | `C:/Users/yyyyc/AppData/Local/hermes/hermes-agent/venv/Lib/site-packages;D:/proj/semantica` |
| ASG-OutputGuard | 8903 | `D:/proj/agent-security-gateway` | `C:/Users/yyyyc/AppData/Roaming/uv/python/cpython-3.11-windows-x86_64-none/python.exe` | `D:/proj/agent-security-gateway/intelligence/outputguard/sidecar.py --port 8903` | `C:/Users/yyyyc/AppData/Local/hermes/hermes-agent/venv/Lib/site-packages` |
| ASG-Gateway | 8090 | `D:/proj/agent-security-gateway` | `D:/proj/agent-security-gateway/bin/gateway.exe` | `serve -config deploy/config.dev.yaml` | — |
| ASG-Cpolar | — | `D:/proj/agent-security-gateway` | `D:/cpolar/cpolar.exe` | `start asg-console --log=stdout` | — |

Notes:
- Behavior sidecar `policy.iv` must be absolute path when service cwd ≠ analyzer; `run_sidecar.sh` uses `cd analyzer` then relative path, so nssm AppDirectory is critical.
- KGWorker worker-token must NOT be hard-coded; use env var `ASG_KG_WORKER_TOKEN` (fallback shown is live token at time of capture).
- OutputGuard and Behavior require `PYTHONPATH` to include hermes venv site-packages for `invariant` import (uv base python alone lacks it).

## Task 11 — nssm install ✅

- `winget install --id NSSM.NSSM --accept-source-agreements --accept-package-agreements` → **Successfully installed** `NSSM 2.24-101-g897c7ad`
- Package path: `C:/Users/yyyyc/AppData/Local/Microsoft/WinGet/Packages/NSSM.NSSM_Microsoft.Winget.Source_8wekyb3d8bbwe/nssm-2.24-101-g897c7ad/win64/nssm.exe`
- Copied to `D:/tools/bin/nssm.exe` (368640 bytes) and added to PATH (`D:/tools/bin` + `WinGet/Links`); `D:/tools/bin/nssm.exe version` → `NSSM 2.24-101-g897c7ad 64-bit 2017-04-26`
- `where nssm` initially empty because winget Links not yet on PATH for current shell — resolved by copying to `D:/tools/bin`.

Fallback ready: if winget had failed, `curl https://nssm.cc/release/nssm-2.24.zip → D:/tools/bin`; if nssm completely unavailable, `sc failure <name> reset=60 actions=restart/5000` (documented in `install-services.ps1` sc branch).

## Task 12 — five-process service definitions ✅ (scripts updated, install needs admin)

`scripts/install-services.ps1` now defines **5 services** (was 3, missing 8901/8903):

| Service | Port | Application | AppDirectory | AppParameters |
|---|---|---|---|---|
| ASG-Gateway | 8090 | `D:/proj/agent-security-gateway/bin/gateway.exe` | `D:/proj/agent-security-gateway` | `serve -config deploy/config.dev.yaml` |
| ASG-Cpolar | — | `D:/cpolar/cpolar.exe` | `D:/proj/agent-security-gateway` | `start asg-console --log=stdout` (subdomain `asg-gateway`, region `cn_vip`, from `~/.cpolar/cpolar.yml`) |
| ASG-Behavior | 8901 | `.../uv/.../python.exe` | `D:/proj/agent-security-gateway/intelligence/analyzer` | `.../sidecar.py --policy .../policy.iv --port 8901` + `AppEnvironmentExtra PYTHONPATH=.../hermes/.../site-packages LOCAL_POLICY=1` |
| ASG-KGWorker | 8902 | `.../uv/.../python.exe` | `D:/proj/agent-security-gateway` | `.../asg_kg_worker.py --port 8902 --semantica-path D:/proj/semantica --worker-token $env:ASG_KG_WORKER_TOKEN` + `PYTHONPATH=...;D:/proj/semantica` |
| ASG-OutputGuard | 8903 | `.../uv/.../python.exe` | `D:/proj/agent-security-gateway` | `.../outputguard/sidecar.py --port 8903` + `PYTHONPATH=...` |

Per-service nssm settings: `AppExit Default Restart`, `AppRestartDelay 5000`, `AppStdout/Stderr → logs/<name>.log`, `AppRotateFiles 1`, `AppRotateOnline 1`, `AppRotateBytes 10485760` (10 MB), `Start SERVICE_AUTO_START`, `DependOnService ""`.

`scripts/uninstall-services.ps1` now removes 5 names + legacy `ASG-Connect` if present; handles both nssm and sc paths.

Verification (syntax & params, no admin execution):
```
powershell -NoProfile -Command "[System.Management.Automation.Language.Parser]::ParseFile(...)"
→ ParseFile OK
Get-Service ASG-* → (none yet — install requires admin, see Task 13)
```

**Manual install (管理员 PowerShell):**
```powershell
powershell -ExecutionPolicy Bypass -File scripts/install-services.ps1
# verify
Get-Service ASG-* | Format-Table -Auto   # expect 5 Running/Automatic
curl http://127.0.0.1:8090/healthz; curl http://127.0.0.1:8901/health; curl http://127.0.0.1:8903/health
```

## Task 13 — kill recovery (<30s) ⏳ 需管理员手动执行

Current status without nssm services: standalone processes (8090:24028, 8901:63536, 8902:44896, 8903:18304, cpolar:27308) are running, each port exactly 1 listener, health 200. But auto-restart (30 s recovery判据) requires nssm services (`AppRestartDelay 5000`).

**需管理员在提升的 PowerShell 中执行验证（cpolar 固定域名 `asg-gateway.vip.cpolar.cn`，重启后不变）：**
```powershell
# 1. 以管理员身份安装 5 服务（会先停止独立进程并接管端口）
powershell -ExecutionPolicy Bypass -File scripts/install-services.ps1
Get-Service ASG-* | Format-Table Name,Status,StartType  # 5 Running

# 2. 逐个 kill 并计时恢复（<30s 判据，不得改判据凑达标）
foreach ($svc in "ASG-Gateway","ASG-Cpolar","ASG-Behavior","ASG-KGWorker","ASG-OutputGuard") {
  $procName = switch ($svc) { "ASG-Gateway" {"gateway"} "ASG-Cpolar" {"cpolar"} default {"python"} }
  # for python services kill by service-specific PID
  $before = (Get-NetTCPConnection -LocalPort 8901,8902,8903,8090 -State Listen -EA SilentlyContinue | Measure-Object).Count
  Write-Host "Killing $svc ..."
  $t0 = Get-Date
  # 获取服务对应的进程并 kill
  if ($svc -eq "ASG-Gateway") { Stop-Process -Name gateway -Force -EA SilentlyContinue }
  elseif ($svc -eq "ASG-Cpolar") { Stop-Process -Name cpolar -Force -EA SilentlyContinue }
  else {
    $port = switch ($svc) { "ASG-Behavior" {8901} "ASG-KGWorker" {8902} "ASG-OutputGuard" {8903} }
    $pidToKill = (Get-NetTCPConnection -LocalPort $port -State Listen -EA SilentlyContinue).OwningProcess
    if ($pidToKill) { Stop-Process -Id $pidToKill -Force -EA SilentlyContinue }
  }
  do { Start-Sleep 2; $svcObj = Get-Service $svc -EA SilentlyContinue; $running = $svcObj.Status -eq "Running" } until ($running -or ((Get-Date)-$t0).TotalSeconds -gt 40)
  $elapsed = ((Get-Date)-$t0).TotalSeconds
  "$svc : ${elapsed:N1}s $(if($elapsed -lt 30){'PASS'}else{'FAIL >30s'})"
  if ($elapsed -ge 30) { Write-Host "STOP — 恢复超时，不得改判据" -ForegroundColor Red; break }
}
# 3. cpolar 域名保持
curl.exe -s https://asg-gateway.vip.cpolar.cn/healthz   # expect 200
```

Expected: 5× `<30 s` (nssm `AppRestartDelay 5000` → ~5 s), any `≥30 s` → stop and report numbers. Result to be appended here and committed after manual run.

Standalone fallback before service化 is healthy (all ports 1 listener/health 200), so the blocker is solely elevation.

---

## Task 0b/0c — Transport A/B 基准与 L1/L2 超时选型依据（实测锁定 2026-08-28）

> 本节为 Task 0b 交付物：所有超时必须有实测数字支撑，明确写出 p50/p90/max 来源与误拒率计算，不得拍脑袋。详细推导见 `docs/DESIGN-V1.md §4.7`，本节为验证侧原始数据归档。

### 实测基线（n=20，同一时刻、同 `curl --max-time 15 -w "%{time_total}"`、fresh-process 模拟 `asg-guard`）

**原始数据** `C:/Users/yyyyc/AppData/Local/Temp/cf.txt` / `cp.txt`（已落盘）：

`cf.txt` (CF `indie-saturn-necklace-icq.trycloudflare.com` via WSL+sjc05 quic, s):
```
1.102644 0.873400 0.879712 0.879440 0.873331 0.867131 0.831392 0.839557 1.375079 0.856175
0.868035 0.854080 5.402048 0.823348 0.829115 0.895225 0.871577 0.841035 0.857667 1.381323
```
`cp.txt` (cpolar `asg-gateway.vip.cpolar.cn`, s):
```
1.666069 1.514892 2.820453 1.469892 1.509282 1.462171 2.112442 2.026206 1.529655 2.942263
1.536181 1.431214 1.662819 1.665579 5.403895 1.510153 1.515813 2.980770 1.585796 1.895337
```

**统计（含 outlier）**：

| 链路 | n | min | p50 | p90 | max | mean | 去异常(n=19) p50 | 去异常 p90 |
|------|---|-----|-----|-----|-----|------|----------------|-----------|
| **CF Tunnel (sjc05, quic)** | 20 | 0.823 | **0.872** | **1.381** | 5.402 | 1.150 | 0.868 | 1.375 |
| **cpolar (vip.cpolar.cn)** | 20 | 1.431 | **1.663** | **2.981** | 5.404 | 2.012 | 1.536 | 2.820 |

- CF 快 **~47%** (p50) / **~54%** (p90)，但 **p50 0.872 未达 <0.3s 判据**（`tls-tcp` 见下）。
- 历史对照：cpolar 最终基线 p50=1.89/p90=3.74 量级一致；本次略好（时段波动）但仍同量级。

**分段拆解** `curl -w "dns=%{time_namelookup} tcp=%{time_connect} tls=%{time_appconnect} ttfb=%{time_starttransfer}"`：

| 链路 | dns | tcp | tls | ttfb | tot | tls-tcp | ttfb-tls |
|------|-----|-----|-----|------|-----|---------|----------|
| CF 1 | 0.0086 | 0.0093 | 0.427 | 0.897 | 0.898 | **0.418** | 0.470 |
| CF 2 | 0.0090 | 0.0099 | 0.435 | 0.907 | 0.908 | **0.426** | 0.472 |
| CF 3 | 0.0079 | 0.0086 | 0.433 | 0.935 | 0.935 | **0.424** | 0.502 |
| cpolar 1 | 0.0079 | 0.0086 | 1.359 | 2.077 | 2.078 | **1.351** | 0.719 |
| cpolar 2 | 0.0078 | 0.0086 | 0.788 | 2.448 | 2.448 | **0.780** | 1.660 |
| cpolar 3 | 0.0081 | 0.0088 | 1.867 | 2.588 | 2.588 | **1.858** | 0.721 |

- **TCP 9ms 复现**：两链路 `time_connect` 均 ~9ms，证明客户端到边缘物理近（LA 出口→sjc05），不是 TCP 瓶颈。
- **TLS 地板 0.423s**：CF `tls-tcp avg 0.423s`，对照组 `cloudflare.com` 0.399/0.836s、`trycloudflare.com` 0.396/0.411s、`google.com` 0.406s — **0.42s 是本机 fresh TLS 普遍地板**，非 CF 独有；`wintun/tun2socks` 代理的 TLS 终结/复加解密固定成本。
- cpolar `tls-tcp avg 1.33s` 且 jitter 大（0.78–1.86s），控制面绕行是主因；本地 `/api/activity` BLOCK 全链路 p50=0.016s，证明隧道占 >99%。

**判定**：`p50 <0.3s 且 tls-tcp <0.1s → CF 主入口` — **CF 0.872/0.423 双未达标，不晋升主入口**；保持 **cpolar 主入口**，CF 作一次性测速（`trycloudflare.com` 随机域名已 kill，不固化）。

### 推导逻辑

- 目标：误拒率 `P(RTT > 超时) <1%` → 超时 > p99。
- n=20 估算：`p99 ≈ max`（20 样本最大值≈95%分位，保守当 p99）：cpolar p99≈5.404s，CF p99≈5.402s（含异常）。
- fail-open (L1) vs fail-closed (L2)：L1 超时放行，误拒无代价，可取接近 p50 偏保守值；L2 超时拒绝，必须覆盖 p90 以上。
- **fresh TLS 地板 0.42s 使 0.3s 不可达**：0.42s 在所有外网 fresh TLS 中复现，且 `requests.Session` 复用 5 次仍 0.43s，说明不是“每次新握手”可省，而是每请求固定成本（代理/QUIC 封装）。`asg-guard` 新进程模型吃不到复用红利，**p50<0.3s 只能靠 HTTP hook 连接池**（`type:"http"`，CC 进程自持连接，省 0.42s）实现。

### 误拒率估算

| 传输 | L2 | p90 | max(≈p99) | P(RTT>L2) | 结论 |
|------|----|-----|-----------|-----------|------|
| cpolar 主入口 L2=5s | 5s | 2.981 | 5.404 | **≈1/20=5%** | 不满足 <1%，但已是最小可用值；p90 已覆盖，剩余 5% 长尾靠 `ASG_BYPASS=1` 逃生舱 + 补报缓解；HTTP hook 可降至 2s 且 <1% |
| CF 备用 L2=3s | 3s | 1.381 | 5.402(异常)/去异常 1.375 | 含异常 5%，去异常 0% | 3s 远超去异常 p90，<1%（若无 5.4s 异常） |
| LAN L2=2s | 2s | 0.003 | 0.003 | 0% | 500×余量 |
| HTTP hook 预期 L2=2s | 2s | ~0.4s | ~1.0s | <1% | 连接复用省 0.42s 后接近 LAN |

L1 cpolar 3s 覆盖 p90=2.98 边际（`P>3s≈10%`），但 fail-open 仅多等 3s 后放行，不阻塞用户，可接受。

### 锁定超时表（与 DESIGN-V1 §4.7.4 一致，`asg-probe-transport`/`asg-guard` 已同步）

| 传输 | p50 | L1 (fail-open) | L2 (fail-closed) | Hook | 依据 |
|------|-----|----------------|------------------|------|------|
| **LAN** | 0.002s | **1s** | **2s** | 4s/5s | 500×余量，误拒 0% |
| **CF Tunnel**（备用） | 0.872s | **2s** | **3s** | 5s/6s | L1>L2 覆盖去异常 p90，含异常~5% |
| **cpolar（主入口）** | **1.663s** | **3s** | **5s** | 6s/8s | L1 刚覆盖 p90，L2 覆盖 p90 但对 max 5.4s 仍~5%误拒，**文档与 `deny_unreachable` 中已明确“误拒率约 5%，靠 `ASG_BYPASS=1` 逃生舱缓解，且 HTTP hook 可降至 1s 以内”** |
| **HTTP hook**（复用预期） | ~0.3–0.5s | **1s** | **2s** | 4s/5s | 省 0.42s 地板后接近 LAN，<1% |

- 所有超时 `env ASG_L1_TIMEOUT`/`ASG_L2_TIMEOUT` 最高优先级覆盖；`asg-guard` 默认 3/5（cpolar 主入口），`asg-probe-transport` 按 URL 自动择优 LAN 1/2 → HTTP 1/2 → CF 2/3 → cpolar 3/5。
- 复测指令见 `docs/DESIGN-V1.md §4.7` 与 `bin/cloudflared` 保留二进制。

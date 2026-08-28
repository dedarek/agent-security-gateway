# ASG 下一步方案 v1（定稿）

> 定稿时间：2026-08-28 北京时间
> 代码基线：`6f12d97`
> 依据：40 问用户答复 + Claude Code telemetry 污染根因调研
> 本文件是**决策记录 + 技术选型出处 + 实施顺序**。每项选型标注**来自哪个仓库/组件/以什么方式介入/可配置成什么样**。

---

## 0. 产品定位（用户定调，不可动摇）

| 维度 | 结论 |
|---|---|
| 第一目标 | **管控**（拦截）。可见性第二，但同样重要。 |
| 目标用户 | **SaaS 形态，为 agent 提供安全防护**（多租户）。 |
| 拦截范围 | **所有操作**：MCP 工具 + 普通 tools + 敏感操作额外把关。 |
| 差异化卖点 | **事后 semantic 知识图谱 / 本体论分析** + solid 的 agent 安全防护。 |
| 接入红线 | **能不改用户配置就不改**。指向 base_url 侵入太深——agent 频繁切换模型来源时要多改一层，维护成本高。但**仍然要拿到真实模型**。 |
| 安装约束 | 远端**只装一次**，之后完全无感。hook 或探针都可以。 |
| 安装形式 | **手工可读的分步命令**，交给 agent 自己执行。不用 `curl \| sh`。 |
| 回滚 | **必须支持**。 |

---

## 1. Claude Code 遥测污染根因（调研结论）

### 1.1 现象
两次开启 `CLAUDE_CODE_ENABLE_TELEMETRY=1` 后，Claude Code 输入框出现无限乱码，必须撤销配置才能恢复。

### 1.2 根因（双重）

**根因 A — OTel 诊断日志抢占 tty（这是我们踩的坑）**
- Claude Code TUI 基于 **Ink**（React for CLI），靠 ANSI 光标控制序列精确重绘界面。
- OpenTelemetry JS SDK 的诊断日志器 `diag` **默认级别 `INFO`**，通过 `DiagConsoleLogger` 直接写 `console`（stdout/stderr）。
  - 出处：`open-telemetry/opentelemetry-js` — `@opentelemetry/sdk-node` 文档「Configure log level from the environment：`OTEL_LOG_LEVEL`，默认 `INFO`」。
- 两者共用同一个 tty → OTel 每写一行日志，Ink 记录的光标位置就失效 → 重绘错位 → 输入框被覆盖成乱码。
- **我们前两次配置都没有设 `OTEL_LOG_LEVEL`**，等于默认让 SDK 往终端喷日志。

**根因 B — console exporter 直接打印遥测到 stdout**
- `OTEL_METRICS_EXPORTER=console` 是官方调试用法，会**每个导出周期把指标对象打印到终端**。
  - 出处：Claude Code 官方文档 monitoring-usage §"console exporter prints metrics to your terminal every second"。
- 若 exporter 未显式钉死为 `otlp`，回退到 `console` 即刻污染界面。

### 1.3 正确配置（修正版）

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_LOG_LEVEL=none            # ← 关键：关闭 SDK 诊断日志，缺这条必炸
export OTEL_METRICS_EXPORTER=otlp     # ← 显式钉死，绝不能回退 console
export OTEL_LOGS_EXPORTER=otlp
export OTEL_TRACES_EXPORTER=none      # traces 需 CLAUDE_CODE_ENHANCED_TELEMETRY_BETA，不用
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=<hub>        # 裸 base，SDK 自行拼 /v1/logs /v1/metrics
export OTEL_METRIC_EXPORT_INTERVAL=60000
```

### 1.4 但 OTLP 仍然**不能作为主干**

即使配置修正，Claude Code 的 OTLP 实现有长期稳定性问题（均为 anthropics/claude-code 官方 issue）：

| Issue | 现象 |
|---|---|
| #50567 | `OTEL_METRICS_EXPORTER=otlp` 静默 no-op —— OTLP exporter 包未打进 bundle |
| #32699 | v2.1.72 自动更新后 telemetry 停止工作（bundle 瘦身导致初始化顺序变化） |
| #13803 | 2.64–2.67 连续版本 telemetry 完全不产出 |
| #66401 | v2.1.153/169 交互式 TUI 下静默不发（`claude -p` 非交互也有 #46338） |

**结论**：OTLP 降级为**可选补充通道**（对 OpenCode 等实现稳定的 harness 有效），**主干走 Hook**。

---

## 2. 接入架构：三通道并存 + 明确降级链

```
                    ┌──────────────────────────────────────┐
                    │   ASG Hub (Gateway :8090)            │
                    │   /api/activity   ← 通道1 Hook       │
                    │   /v1/* (LLM)     ← 通道2 Proxy      │
                    │   /v1/logs|metrics← 通道3 OTLP       │
                    │   /mcp            ← 通道4 MCP        │
                    └──────────────────────────────────────┘
                              ▲         ▲          ▲
        ┌─────────────────────┘         │          └──────────────┐
        │                               │                         │
  通道1 Hook（主干）              通道2 Proxy（增强）         通道3 OTLP（补充）
  改 harness 配置文件            改 base_url（可选）        改环境变量（可选）
  零侵入运行时                   拿全量请求/响应             拿 token/cost 指标
  拿工具链路 + 会话              拿模型真值                 harness 实现不稳
  拿不到模型真值                 与 cc-switch 冲突          Claude Code 不可靠
```

### 2.1 通道选择矩阵（按 harness）

| Harness | 主通道 | 模型真值来源 | 备注 |
|---|---|---|---|
| Claude Code | **Hook**（`PostToolUse`/`SessionStart`/`Stop`） | hook payload + 可选 OTLP logs | OTLP 需带 `OTEL_LOG_LEVEL=none` |
| OpenCode | **Plugin**（已验证 OTLP 稳定） | OTLP span 属性 | 本机 `local-yycserver` 已接 |
| Codex | `notify` 脚本 | hook payload | 待验证 |
| Gemini CLI | extensions | hook payload | 待验证 |
| 自研 / LangGraph 等 | **Proxy**（愿意改 base_url 时） | 请求体 `model` 字段（真值） | 三轴引擎全量生效 |

### 2.2 降级链

1. 优先 Hook（不碰任何运行时配置）。
2. 用户愿意改 base_url → 叠加 Proxy，**升级为全量管控**（三轴引擎生效 + 模型真值）。
3. harness OTLP 实现稳定 → 叠加 OTLP，补充 token/cost 指标。
4. 三者都失败 → 仅注册 + 心跳，显示 `registered, no telemetry`。

### 2.3 模型真值策略（用户已接受限制）

- 前面挂了 cc-switch 这类路由代理时，ASG **无法**知道真值 —— 用户明确接受。
- **退一步方案（用户同意）**：显示模型 + **来源标注**
  - `self-reported`（harness 声称，灰色标记）
  - `gateway-observed`（走 Proxy 通道，网关亲眼所见，绿色标记）
- **不做模型指纹探测**（用户明确否决）。
- **模型变更历史要做**（用户明确要求）：时间线 + "检测到模型切换" 事件。

---

## 3. 技术选型明细（仓库 / 组件 / 介入方式 / 可配置项）

### 3.1 接入层

#### 3.1.1 Hook 分发器（零二进制 / 零 sudo / 零常驻进程）

> **部署形态定案**：Hook 通道**不下载任何二进制、不需要 sudo、不写系统目录、不留常驻进程、不碰 shell 配置、不设环境变量**。
> 安装物 = `~/.asg/asg-report`（POSIX sh 脚本）+ `~/.asg/config`。依赖仅 `sh` + `curl`。
> 完整交付文案见 `DESIGN-V1.md` §2.9。

- **来源**：自研，但 hook 规范来自各 harness 官方文档
  - Claude Code：`https://code.claude.com/docs/en/hooks` — `PreToolUse` / `PostToolUse` / `SessionStart` / `SessionEnd` / `Stop`，`type:command`，`async:true`
  - OpenCode：`opencode.jsonc` 的 `plugin` 数组
  - Codex：`notify` 配置项
- **介入方式**：探测 harness 配置文件存在性 → **JSON 合并**（非覆盖）写入 hook 段 → 原文件备份为 `<file>.asg-backup-<ts>`
- **安装位置**：`~/.asg/`（用户家目录，无权限问题）；探针二进制（仅 Proxy 通道需要）装 `~/.asg/bin/`，同样不进 `/usr/local/bin`
- **可配置项**：
  ```
  hub_url         上报地址
  agent_id        machine-id + harness 名（用户答复：这么定）
  tenant_key      租户密钥
  events          要挂哪些 hook（默认 PostToolUse + SessionStart + Stop）
  detail_level    minimal(仅活动) | tool(含工具名) | full(含参数摘要)
  sample_rate     采样率，默认 1.0
  ```
- **三条硬规则**（违反任一都发生过事故）：脚本不得写 stdout/stderr；观察路径永远 `exit 0`；curl 有界超时 2s
- **回滚**：还原备份 + 精确移除 ASG hook 项 + `rm -rf ~/.asg`（用户要求必须有）

#### 3.1.2 Proxy 通道（现有 `cmd/asg-connect serve`）

- 保留现状，但**必须补测试**（当前 3% 覆盖率，"永远 hy3" bug 就出自这里）
- `route()` 已改为透传，不再静默重映射

#### 3.1.3 OTLP 通道

- 现有 `internal/otlp`（手写 protobuf 解码，466 行，零依赖）**保留**
- 补：`/v1/logs` 深度解码，提取 Claude Code `api_request` 事件里的 `model` / `session.id` / `cost` / `token`
  - 出处：Claude Code 官方文档 monitoring-usage 的 Events 表

### 3.2 存储层（用户答复：可以换，按条数上限轮转）

| 组件 | 仓库 | 介入方式 | 配置 |
|---|---|---|---|
| **SQLite** | `modernc.org/sqlite`（**纯 Go，无 CGO**，Windows 交叉编译友好） | 替换 `internal/store`，建表 events / agents / model_history / policy_hits，加索引 | `max_events`（条数上限轮转）、`db_path` |
| **审计原件** | 保留 `events.jsonl` 追加式（用户答复：合规友好要保留） | 双写：SQLite 供查询，JSONL 供审计 | `audit_jsonl_enabled` |
| **agents.json** | 并入 SQLite（用户答复：原子写或直接并入都可以） | 删除全量重写路径，根除 BOM 崩溃 | — |
| **签名回执** | **砍掉**（用户答复：不需要） | 移除 `internal/receipt` 及 `/api/receipts*` | — |

### 3.3 策略与引擎（用户答复：三轴必须接到 LLM 路径，不能只在 MCP）

| 轴 | 组件 | 仓库 | 状态 | 动作 |
|---|---|---|---|---|
| A 权限 | Cedar | `cedar-policy/cedar-go` v1.8.0 | 已跑 | 保留 |
| A 权限（补） | OPA/Rego | `open-policy-agent/opa` | 未引 | **允许引入**（用户答复：不必坚持单一语言，效果优先） |
| B 数据/网络 | Pipelock 规则包 | `deploy/rules/pipelock-community.yaml` | 已跑 | 保留 |
| C 行为/因果 | Invariant | `invariantlabs-ai/invariant` | **桩，未真跑** | **必须落地**（用户答复：要落地）。Python sidecar `:8901` |
| 输出安全 | **llm-guard** | `protectai/llm-guard` | 未引 | **引入**（用户答复：引入，Python 依赖没关系）。prompt injection / PII / toxicity |

**关键改造（P0）**：三轴引擎目前只挂在 `/mcp` 路径的 `proxy.Gateway` 上。必须把同一套 `engine.Registry` 接到 **LLM 路径**（`asg-connect serve` 的 `handleLLM` + 网关 `/v1/*` facade），使 prompt / 工具调用 / 响应内容都过策略。

### 3.4 策略动作（用户答复：记录/告警/拦截都要，且要能对不同 agent 分别制定）

- **每 agent 策略**：控制台可为每个 agent 单独配置策略集
- **动作**：`log` / `alert` / `block`
- **拦截时 agent 看到什么**：**根据触发的问题返回对应错误**（用户明确要求），例如
  ```json
  {"error":{"type":"asg_policy_block","code":"DATA_EXFIL_RISK",
   "message":"Blocked by ASG: tool `send_email` would transmit data tainted by `read_secret`.",
   "policy":"pipelock/exfil-01","trace_id":"..."}}
  ```

### 3.5 在线状态策略（用户答复：让我定）

**定案**：
- **只用活动，不用心跳判在线**（用户倾向"只用调用"，且心跳曾导致"永远 online" bug）
- 心跳保留但**只写 `last_heartbeat`**，用于区分"进程活着但闲置" vs "进程没了"
- 状态三态：
  | 状态 | 判据 | UI |
  |---|---|---|
  | `active` | 5 分钟内有真实活动 | 绿灯 |
  | `idle` | 无活动，但 2 分钟内有心跳（进程还在） | 黄灯 |
  | `offline` | 既无活动也无心跳 | 灰灯 |
- **UI 分两个指示灯**（活动灯 + 进程灯）
- offline **只能手动删除**（用户明确要求）

### 3.6 "正在做什么"（用户要求：非常详细，整个工作链路 + 每步安全信息）

- 数据来源：Hook 的 `PostToolUse` payload（工具名 + 参数摘要）+ Proxy 的请求/响应 + 三轴判定结果
- UI 形态：**工作链路时间线**
  ```
  session: fix-login-bug
  ├─ 10:22:01  Read      src/auth.go              ALLOW
  ├─ 10:22:04  Grep      "password"               ALLOW  ⚠ taint:secret
  ├─ 10:22:09  Edit      src/auth.go              ALLOW
  └─ 10:22:15  Bash      curl -X POST api.x.com   BLOCK  ⛔ exfil-01
  ```

### 3.7 控制台（用户答复：React 正经工程 + 补子系统 UI + 实时推送）

| 项 | 选型 | 出处 |
|---|---|---|
| 框架 | **Vite + React + TypeScript** | 标准前端工程 |
| UI 库 | **已移除 Tailwind —— 实际采用内联样式，2026-08-28 审计确认 className 使用为 0** | — |
| 图表 | **Recharts** | `recharts/recharts` |
| 图谱可视化 | **Cytoscape.js** 或 **react-force-graph** | 用于 semantic KG / 本体论展示（**核心卖点**） |
| 实时推送 | **SSE**（`text/event-stream`，比 WebSocket 简单，单向足够） | 用户答复：要实时推送 |
| 打包 | `go:embed` 构建产物 | 保持单二进制分发 |
| 补 UI 的子系统 | KG / judge / intel / monitor（用户答复：要补） | — |
| 告警出口 | **先不接外部**，只在前端展示（用户答复） | — |

### 3.8 进程自愈（用户答复：四个进程要自愈）

- **Windows**：`nssm`（Non-Sucking Service Manager）或原生 Windows Service 包装
  - 托管：`gateway.exe`、`asg-connect.exe`、`cpolar`、`kg-worker`
  - 配置：崩溃自动重启、开机自启、日志轮转
- **公网入口**：**继续 cpolar**（用户答复：cpolar）

### 3.9 测试策略（用户答复：功能优先，测试跟进）

- 不阻塞功能开发，但**每个新模块必须带测试**
- 存量 14 个零测试包，按"被改到才补"的原则跟进
- **例外**：`cmd/asg-connect` 覆盖率 3% 且承载全部 LLM 流量，**必须优先补到 60%+**（这里出过生产 bug）

---

## 4. 实施顺序（里程碑 + 可证明的验收判据）

### M1 · 接入闭环（1 周内）
1. `asg-connect init` hook 分发器（Claude Code / OpenCode 两种）
2. `asg-connect uninstall` 回滚
3. `/api/activity` 扩展：接收工具名 + 会话 + 链路
4. 在线三态（active / idle / offline）
- **验收**：远端 Mac 执行分步命令后，正常使用 Claude Code，控制台出现该 agent 的**工具调用链路**，5 分钟内活动则绿灯，退出后转灰。**全程未改 base_url、未设 OTEL 变量、未污染输入框。**

### M2 · 存储与显示（1 周）
1. SQLite（`modernc.org/sqlite`）替换 store，条数上限轮转
2. `agents.json` 并入，根除 BOM 崩溃
3. 模型变更历史 + 来源标注（self-reported / gateway-observed）
4. 砍掉 `internal/receipt`
- **验收**：10 万条事件下查询 < 100ms；杀进程重启无数据损坏。

### M3 · 管控落地（2 周，**核心里程碑**）
1. 三轴引擎接入 LLM 路径（不只 MCP）
2. Invariant 行为轴 sidecar 真正跑起来
3. 引入 `protectai/llm-guard`
4. 每 agent 策略配置 + 三种动作（log/alert/block）
5. 拦截错误按触发问题返回结构化错误
- **验收**：构造一次"读密钥 → 外发"的攻击链，agent 侧收到明确的 `asg_policy_block` 错误，控制台显示完整链路 + 命中策略。

### M4 · 控制台重构（2 周）
1. Vite + React + shadcn 重写
2. SSE 实时推送
3. KG / judge / intel / monitor 四个子系统补 UI
4. **semantic 图谱 / 本体论视图**（核心卖点，重点投入）
- **验收**：控制台不再是单文件 HTML；图谱视图可交互展示 agent 行为本体论。

### M5 · 运维加固（1 周）
1. nssm 托管四进程 + 自愈
2. `cmd/asg-connect` 测试覆盖到 60%+
- **验收**：手动 kill 任一进程，30 秒内自动恢复。

---

## 5. 与用户既定规则的一致性检查

| 规则 | 本方案是否遵守 |
|---|---|
| 只显示注册 Agent，不自采集 | ✅ `/api/activity` 与 OTLP 均校验已注册 |
| 离线显示 offline 不消失 | ✅ 三态设计，仅手动删除 |
| 所有时间北京时间 | ✅ 沿用 `fmtBeijing` |
| 在线 = 5 分钟真实活动，心跳不算 | ✅ 心跳只写 `last_heartbeat` |
| 模型必须透传，禁写死 | ✅ `route()` 已改透传，`allowed_models`/`model_map` 已删 |
| 一行/分步接入，之后无感 | ✅ hook 装一次，零运行时侵入 |
| 远端不跑额外组件、不改配置、不发探测 | ✅ hook 仅改 harness 配置文件一次；不发探测（已否决指纹） |
| 不造轮子，先查高星方案 | ✅ Cedar / Invariant / llm-guard / modernc-sqlite / shadcn 均为成熟开源 |
| 免费零成本优先 | ✅ 全部开源；cpolar 沿用现有 |

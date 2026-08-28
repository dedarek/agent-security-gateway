# ASG 下一步方案（工作稿 v0 — 待 40 问答复后定稿）

> 生成时间：2026-08-28 北京时间
> 代码基线：`9e5c276`，Go 12,844 行，37 个包，1 个 HTML 控制台（1,174 行）
> 本文件是**决策记录**：现状体检 → 问题清单 → 候选技术选型（含仓库/组件/接入方式/可配置项/连接关系/场景矩阵）。
> 未定项标注 `❓Qnn`，对应下方 40 问；答复后本文件升级为 v1 定稿。

---

## 第 1 部分 · 现状体检（事实，不含判断）

### 1.1 代码分布

| 包 | 行数 | 测试文件 | 覆盖率 | 定位 |
|---|---|---|---|---|
| `internal/webui` | 2601 | 4 | 32.5% | 控制台 API + OTLP + public facade |
| `cmd/asg-connect` | 2218 | 1 | **3.0%** | 探针（LLM 代理 + 注册 + 心跳 + 上报） |
| `internal/engine` | 1046 | 1 | 30.0% | 三轴引擎（permission/datanetwork/taint/behavior） |
| `internal/agentregistry` | 591 | 1 | 77.9% | Agent 注册表（在线判定/模型观测/会话） |
| `internal/otlp` | 466 | 1 | 77.1% | 手写 protobuf 解码 |
| `cmd/gateway` | 441 | **0** | — | 进程装配 |
| `internal/outputsafety` | 439 | 1 | 16.8% | 输出安全 |
| `internal/proxy` | 417 | 1 | 20.2% | MCP 决策管道 |
| `internal/ingress` | 324 | **0** | — | MCP 接入 |
| `internal/receipt` | 271 | **0** | — | 签名回执 |
| `internal/intel` | 224 | **0** | — | 情报 |
| `internal/monitor` `judge` `store` `session` `registry` `kg` `policyversion` `authn` `approval` `escalation` `mcpproxy` | 各 95–185 | **0** | — | 全部零测试 |

**零测试且 >90 行的包共 14 个**，合计约 2,100 行。

### 1.2 HTTP 路由全表（44 条）

```
控制台/鉴权   /  /login  /api/ui-login  /healthz  /explorer  /explorer/
Agent 生命周期 /api/agents  /api/agents/register  /api/agents/heartbeat
              /api/agents/detail  /api/agents/action  /api/activity   ← 新增
遥测接收      /v1/traces  /v1/logs  /v1/metrics                      ← 新增
LLM 门面      /v1/  /v1/messages  /v1/responses  /responses
MCP           /mcp
决策/事件     /api/ingest  /api/events  /api/sessions  /api/query  /api/status
              /api/trajectory  /api/hook-check
策略          /api/policies/current  /history  /rollback
审批/建议     /api/approvals  /api/suggestions  /api/suggestion/decide
知识图谱      /api/kg/ask  /search  /graph/nodes  /graph/edges  /graph/path
其他          /api/receipts  /verify  /api/registry  /sync  /api/siem
              /api/judge/findings  /api/monitor/findings  /api/clusters
```

### 1.3 依赖（极简，这是优点）

```
github.com/cedar-policy/cedar-go v1.8.0        权限策略引擎（AWS 官方 Go 实现）
github.com/modelcontextprotocol/go-sdk v1.7.0  MCP 官方 Go SDK
github.com/google/uuid  gopkg.in/yaml.v3
```
OTLP protobuf **手写解码**，零依赖（`internal/otlp/receiver.go` 466 行）。

### 1.4 持久化现状

```
data/agents.json        6 KB    全量重写（无原子 rename，曾被 BOM 写坏导致启动崩溃）
data/events.jsonl       1.2 MB  仅 append，无轮转、无索引、无 TTL
data/receipts.jsonl     13 KB
data/mcp-registry.json  158 B
+ 4 个手工 .bak 残留在 data/ 目录（未 gitignore 分类）
```

### 1.5 接入通道现状（本轮踩坑总结）

| 通道 | 端点 | 状态 | 已知问题 |
|---|---|---|---|
| 探针反代（OpenAI 兼容） | `asg-connect:8181` → `/v1/chat/completions` | 可用 | 只对"愿意改 base_url"的 harness 有效；Claude Code 已被 cc-switch 占用 base_url |
| OTLP traces | `:8090/v1/traces` | 可用 | OpenCode 插件已验证；Claude Code 默认不发 traces（beta flag） |
| OTLP logs/metrics | `:8090/v1/logs` `/v1/metrics` | **新增，仅 ACK 不解码** | 未提取模型/会话，只刷活动 |
| Hook 信标 | `:8090/api/activity` | **新增，已端到端验证** | 靠 harness 的 hook 机制；每种 harness 写法不同 |
| MCP 代理 | `:8090/mcp` | 可用 | 只覆盖 MCP 工具调用，不覆盖 LLM 调用 |

**本轮结论（血的教训）**：
- `CLAUDE_CODE_ENABLE_TELEMETRY=1` 在用户这版 Claude Code 上会**污染输入框**（两次复现），OTLP 路线对 Claude Code **不可用**。
- 不能依赖 cc-switch（用户明确否决：要通用性、泛化性）。
- 唯一在 Claude Code 上验证可行的是**官方 hooks + `async:true` + 后台 curl**。

---

## 第 2 部分 · 问题清单（按严重度）

### P0 — 阻塞产品定位

- **P0-1 接入模型不统一**：现在有 5 条互不相干的接入通道，每条覆盖面不同、语义不同、可靠性不同。用户要的是"一行接入、之后无感"，现状是"每台机器单独调试半小时"。
- **P0-2 探针 3% 覆盖率**：2,218 行、承载全部 LLM 流量、只有 3% 测试覆盖。本轮 `route()` 的静默重映射 bug（永远 hy3）就是这个洞里长出来的。
- **P0-3 观测≠管控**：控制台能看到 agent，但**看不到它在调什么工具、发什么请求**。三轴引擎（`internal/engine`）只在 MCP 路径上生效，而真实 agent 走的是 LLM 路径。**安全网关目前实际只是个"在线状态板"**。

### P1 — 工程风险

- **P1-1 `agents.json` 非原子写**：已经崩过一次（BOM）。需要 `write tmp + rename`。
- **P1-2 `events.jsonl` 无边界**：1.2 MB 且只增不减，无轮转/TTL/索引，查询靠全表扫。
- **P1-3 14 个包零测试**：`ingress` `receipt` `store` `session` `monitor` `judge` 等核心路径。
- **P1-4 `cmd/gateway` 零测试**：441 行装配逻辑，改一次配置就可能起不来。
- **P1-5 单文件前端 1,174 行 HTML**：无构建、无组件、无状态管理，改一处容易碰坏另一处。
- **P1-6 后台进程手工管理**：gateway / asg-connect / cpolar / kg-worker 靠 `Stop-Process` + 手动重启，没有 supervisor。

### P2 — 功能缺口

- **P2-1 OTLP logs/metrics 不解码**：Claude Code 的 `api_request` 事件里带真实模型名，现在丢掉了。
- **P2-2 无模型真值校验**：agent 声称的 model 与实际上游跑的 model 无法交叉验证（本轮核心痛点）。
- **P2-3 无 hook 分发器**：每种 harness 的 hook 配置要手写 JSON，没有 `asg-connect init --app claude-code` 自动写入。
- **P2-4 无告警/通知**：offline、异常模型、策略命中都没有出口。
- **P2-5 KG / judge / intel / monitor 四个子系统未在控制台闭环**：有 API 无 UI 消费。

---

## 第 3 部分 · 候选技术选型（详细版，待确认）

> 原则遵循用户既定偏好：**免费/零成本优先；先查 GitHub 高星成熟方案再定架构；不造轮子；第三方能力声称先核实**。

### 3.1 接入层 —— 三种候选架构

#### 候选 A：Hook-First（当前已验证可行）

**技术来源**
- Claude Code 官方 hooks：`https://code.claude.com/docs/en/hooks` — `PreToolUse` / `PostToolUse` / `SessionStart` / `Stop`，`type:command`，`async:true` 不阻塞主循环。
- OpenCode 插件机制：`@devtheops/opencode-plugin-otel`（已在本机 `opencode.jsonc` 使用）。
- Codex `notify` 脚本钩子。
- Gemini CLI extensions。

**介入方式**：`asg-connect init --app <harness>` 探测已安装 harness，**合并**（非覆盖）写入其配置文件的 hook 段，命令体为
```
curl -s -X POST $HUB/api/activity -d '{...}' >/dev/null 2>&1 &
```

**可配置项**：`hub_url` / `agent_id` / `tenant_key` / 采样率 / 上报字段白名单（是否含 tool 名、参数摘要）。

**优点**：官方支持、不碰 base_url、不碰 API key、已在 Claude Code 上验证不炸。
**缺点**：每种 harness 写法不同（需维护适配矩阵）；只能拿到 harness 愿意给的信息；**拿不到真实模型名**（除非 hook payload 里有）。

#### 候选 B：Proxy-First（探针反代，现状主路径）

**技术来源**：自研 `cmd/asg-connect`（2,218 行）。同类高星方案：
- `BerriAI/litellm`（★20k+，Python，LLM 网关事实标准，支持 100+ provider、callback 钩子、Langfuse/Helicone 集成）
- `maximhq/bifrost`（Go，声称 Claude Code 一行接入 `claude mcp add --transport http bifrost`）
- `Portkey-AI/gateway`（★7k+，TS/Cloudflare Workers，AI gateway + guardrails）
- `Helicone/helicone`（★3k+，proxy 架构，任何 HTTP LLM provider）

**介入方式**：agent 的 `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` 指向探针。

**优点**：能看到**全量请求/响应**，模型真值、token、延迟、内容全在手；三轴引擎可以真正在 LLM 路径上生效（解决 P0-3）。
**缺点**：**要求 agent 改 base_url** —— 与 cc-switch 之类的模型路由代理冲突；一旦探针挂了 agent 就不能用（本轮已发生）。

#### 候选 C：Sidecar-Collector（OTel Collector 标准形态）

**技术来源**
- `open-telemetry/opentelemetry-collector-contrib`（★3k+，官方）— 远端跑一个 collector，agent 发本地 `:4317`，collector 再批量转发到网关，**网络抖动不影响 agent**。
- Elastic 的 Claude Code 监控方案就是这个形态。

**优点**：解耦、有重试/缓冲/批处理、协议标准。
**缺点**：远端要**多跑一个进程**（用户明确拒绝"远端跑额外组件"）。

> ❓Q1–Q8 讨论选哪条主路径 / 是否分场景组合。

### 3.2 数据面 —— 存储与查询

| 方案 | 来源 | 介入方式 | 适配场景 |
|---|---|---|---|
| **当前** JSONL + JSON | 自研 | 直接文件 | 单机、小量 |
| **SQLite (modernc.org/sqlite)** | `modernc.org/sqlite`（★3k+，**纯 Go 无 CGO**，Windows 友好） | 替换 `internal/store`，events/agents/receipts 建表+索引 | 单机万级事件、需要 WHERE/ORDER/聚合 |
| **DuckDB** | `marcboeker/go-duckdb`（需 CGO） | 分析侧只读 | 事后分析、SOC 报表 |
| **ClickHouse** | Helicone 用的就是这个 | 独立服务 | 百万级、多租户 SaaS |

> ❓Q9–Q13 讨论存储演进节奏与规模目标。

### 3.3 策略/规则层

- **已用**：`cedar-policy/cedar-go`（AWS 官方，权限轴）。
- **候选补充**：
  - `invariantlabs-ai/invariant`（行为/因果轴，已有 sidecar 桩 `internal/engine/behavior.go` → `:8901`，**当前未真正跑起来**）
  - `open-policy-agent/opa`（★9k+，Rego，通用策略，Go 原生嵌入）
  - `guardrails-ai/guardrails`（输出安全）
  - `protectai/llm-guard`（★1k+，prompt injection / PII / toxicity 检测）
  - `NVIDIA/NeMo-Guardrails`（★4k+，对话级护栏）

> ❓Q14–Q20 讨论策略引擎收敛、行为轴是否落地、护栏用哪家。

### 3.4 控制台前端

| 方案 | 来源 | 介入方式 |
|---|---|---|
| **当前** 单文件 HTML | 自研 1,174 行 | `go:embed` |
| **htmx + Alpine.js** | 零构建，CDN 引入 | 保持 `go:embed`，服务端渲染片段 |
| **Vite + React + shadcn/ui** | 标准前端工程 | 独立 `web/`，构建产物 `go:embed` |
| **Grafana 面板** | 数据源用 SQLite/Prometheus | 完全外置，网关只出数据 |

> ❓Q21–Q26 讨论控制台定位与技术栈。

### 3.5 进程与部署

- **候选**：`nssm` / Windows Service / `supervisord` / Docker Compose / systemd（远端 Linux/Mac 用 `launchd`/`systemd --user`）。
- **公网入口**：当前 cpolar 专业版固定子域；候选 `cloudflared`（免费固定域名）、`frp`（自建）、`tailscale funnel`。

> ❓Q27–Q31。

### 3.6 一行接入的最终形态（目标态草案）

```bash
curl -fsSL https://asg-gateway.vip.cpolar.cn/install.sh | sh -s -- --key <tenant-key>
```
脚本内部：
1. 探测已安装 harness（`~/.claude` / `~/.config/opencode` / `~/.codex` / `~/.gemini`）
2. 生成稳定 `agent_id`（machine-id + harness 名）
3. **合并**写入各 harness 的 hook/plugin 配置（备份原文件，幂等，可 `--uninstall` 回滚）
4. 向 `/api/agents/register` 注册
5. 自检：发一条 `/api/activity`，确认控制台可见，打印结果

> ❓Q32–Q40 讨论安装脚本的边界、幂等、回滚、多 harness、离线机器、企业分发。

---

## 第 4 部分 · 待定问题索引

见对话中的 40 问；答复后本文件升级 v1，补齐：
- 主路径选型与降级链
- 每个组件的具体版本与接入代码位置
- 场景矩阵（本机/局域网/公网/离线/多 harness/多租户）
- 里程碑与验收标准（每个里程碑的"可证明完成"判据）

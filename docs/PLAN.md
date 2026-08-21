# Agent Security Gateway —— 详细 Step-by-Step 方案

本文件是产品从 0 到 1 的落地路线。分 6 个阶段（Phase 0–5），
每个阶段给出：目标、要做的事、交付物、验收标准、依赖。

阅读顺序建议：先看 [ARCHITECTURE.md](ARCHITECTURE.md) 理解三轴模型，
再看 [OPEN-SOURCE-ANALYSIS.md](OPEN-SOURCE-ANALYSIS.md) 理解为什么自研主干 + Engine 化，
最后回到本文件看执行节奏。

---

## 0. 一句话产品定义

> Agent Security Gateway = Agent 工具调用的**统一安全控制面** + **事后分析闭环**。

拆成两个部件：

- **数据面 (Data Plane) = Gateway**：实时看见调用 → 三轴判断 → 拦截/放行/审批/脱敏。
- **分析面 (Control/Analysis Plane) = Intelligence / SOC**：还原轨迹 → 根因 → 发现模式 → 生成策略建议 → 回写 Gateway。

产品不是 `ToolHive + Pipelock + Invariant` 的拼装，而是：**我们掌握 Gateway 主干，
三个开源项目的能力做成可插拔 Engine/Adapter**。架构不绑死在任一开源项目上。

---

## 1. 核心设计原则（贯穿所有阶段）

1. **三轴正交**：权限轴 / 数据·网络轴 / 行为·因果轴，三条轴都贯穿 Pre / Runtime / Post。
2. **决策统一出口**：所有轴的信号汇入一个 `Risk Decision Engine`，输出统一决策
   `ALLOW / BLOCK / CONFIRM / REDACT`。
3. **Engine 可插拔**：ToolHive / Pipelock / Invariant 都是 Engine 实现，通过统一接口接入，可替换。
4. **闭环优先**：从 Phase 1 起就打通「事件沉淀 → 分析 → 策略回写」的最小闭环，哪怕很简陋。
5. **Fail 策略明确**：每条轴、每个 Engine 都要声明 fail-open 还是 fail-closed。
6. **人在环路 (HITL)**：敏感操作走 `CONFIRM`，人工确认后放行，并记录决策用于学习。

---

## Phase 0 — 立项与骨架（第 1–2 周）

**目标**：把仓库、协议、接口、可编译骨架立起来，团队有共同语言。

### Step 0.1 确定接入协议
- 主接入面选 **MCP (Model Context Protocol)**：Agent 生态正在收敛到 MCP，工具调用语义标准化。
- 网关形态：Gateway 作为 **MCP Proxy**，位于 Agent 与真实 MCP Server 之间。
  - Agent 只连 Gateway 暴露的「虚拟 MCP endpoint」。
  - Gateway 转发到真实 MCP Server（filesystem / github / database / slack …）。
- 兼容非 MCP：提供 HTTP/gRPC `POST /v1/decision` 让自研 Agent 主动送审（sidecar 模式）。

### Step 0.2 定义核心数据模型（最重要，先定死）
统一的 `ToolCall` / `ToolResult` / `Decision` / `Event` schema（见 ARCHITECTURE.md §4）。
放在 `api/proto/`，Go 和 Python 共用生成代码。

### Step 0.3 定义 Engine 接口
```go
type Engine interface {
    Name() string
    Axis() Axis // Permission | DataNetwork | Behavior
    // 三个钩子，Engine 可只实现关心的阶段
    EvaluatePre(ctx, *ToolCall)  (*Signal, error)
    EvaluateRuntime(ctx, *ToolCall, *Stream) (*Signal, error)
    EvaluatePost(ctx, *ToolCall, *ToolResult) (*Signal, error)
}
```
`Signal` = { score 0–100, verdict, reasons[], evidence[], failMode }。

### Step 0.4 可编译骨架
- `cmd/gateway` 能启动、加载配置、注册 Engine、跑一个 passthrough 代理（不做任何拦截）。
- CI（lint + build + test）跑通。

**交付物**：仓库骨架、proto schema、Engine 接口、passthrough 网关、CI。
**验收**：Agent 通过 Gateway 调用一个真实 MCP（filesystem）成功，全程无拦截，事件被打印。

---

## Phase 1 — 权限轴 MVP + 最小闭环（第 3–6 周）

**目标**：跑通「权限轴拦截 + 事件沉淀 + 人工确认」的最小闭环。这是产品的第一个可 demo 版本。

### Step 1.1 身份接入
- Agent / User / Session 身份：支持 API Key + JWT，识别「谁（user）用哪个 agent 发起哪个 session」。
- 工具/资源建模：`tool_id`、`resource`（如 `database.users`）、`action`（read/write/delete）。

### Step 1.2 权限轴 Engine（ToolHive 类）
- 策略引擎选 **Cedar**（AWS 开源，专为授权设计，可读性强）或 **OPA/Rego**。
  - 推荐 Cedar：策略即代码，`principal / action / resource / context` 模型天然贴合。
- 策略示例：
  ```
  // 普通员工 agent 禁止删除用户
  forbid(principal, action == Action::"database.delete_user", resource)
  when { principal.role == "employee" };
  ```
- Engine 在 `EvaluatePre` 做准入判断，`EvaluatePost` 校验结果是否越权 + 留证据。

### Step 1.3 决策引擎骨架
- `Risk Decision Engine` 汇总（此阶段只有权限轴一条）：`ALLOW / BLOCK / CONFIRM`。
- `CONFIRM` 分支：把调用挂起，推送到审批队列，等待人工 `approve/deny`（Slack/飞书/Web）。

### Step 1.4 事件沉淀
- 每次 decision 产出结构化 `Event`，写入事件总线（NATS）+ 持久化（Postgres / ClickHouse）。

### Step 1.5 最小分析闭环
- SOC 侧先做最简单的：把事件按 session 聚合成「轨迹」，Web 上能看到一条 Agent 行为时间线。
- 手动「从某次 BLOCK 生成一条策略草案」→ 管理员确认 → 写回 Cedar 策略文件 → Gateway 热加载。

**交付物**：权限轴 Engine、Cedar 策略、CONFIRM 人工审批、事件流、轨迹视图、策略回写 v0。
**验收（Demo 剧本）**：
1. 员工 agent 调 `database.delete_user()` → 权限轴 `BLOCK`，前端显示拒绝原因。
2. 员工 agent 调 `database.export_all_users()` → `CONFIRM`，管理员飞书点确认后放行。
3. 事件在轨迹视图可见；管理员一键把「禁止 export_all_users」固化为策略，下次直接 BLOCK。

---

## Phase 2 — 数据/网络轴（第 7–10 周）

**目标**：加入 Pipelock 类能力，管住「Agent 调完工具后数据往哪跑」。

### Step 2.1 出网拦截点
- 两种部署形态，都要支持：
  - **Forward Proxy 模式**：Agent 的所有出网 HTTP(S) 走 Gateway 代理，MITM 检查（需信任 CA）。
  - **MCP 内联模式**：对 MCP 工具的参数/返回内容做检查（无需 MITM）。
- 运行时轴：`EvaluateRuntime` 在流式返回时逐块检查。

### Step 2.2 检测能力（数据/网络轴 Engine）
- **SSRF / 恶意域名**：目标 URL 解析，内网地址、云 metadata（169.254.169.254）、
  domain allow/deny list、newly-seen-domain 风险打分。
- **DLP / Secret 外泄**：正则 + 熵检测（AWS key / 私钥 / token / PII），
  参数出方向 & 返回入方向双向扫描。
- **Tool Poisoning**：MCP 工具描述/返回里注入的隐藏指令检测。
- **REDACT 决策**：不一定 BLOCK，可对敏感字段脱敏后放行。

### Step 2.3 决策引擎升级为多轴汇总
- 权限轴 + 数据网络轴并行评估，汇总策略（见 ARCHITECTURE.md §5 汇总算法）。
  - 任一轴 BLOCK → BLOCK；有 CONFIRM 无 BLOCK → CONFIRM；可 REDACT 优先 REDACT。

**交付物**：出网代理、SSRF/DLP/Poisoning 检测 Engine、REDACT、双轴汇总决策。
**验收**：
1. Agent `curl attacker.com` → 数据网络轴 BLOCK。
2. Agent 读 `~/.ssh/id_rsa` 并试图 POST 外网 → BLOCK + 告警。
3. Agent 返回含客户手机号 → REDACT 脱敏后放行。

---

## Phase 3 — 行为/因果轴（第 11–16 周）

**目标**：加入 Invariant 类能力，识别「几步连起来才构成攻击」的行为链。这是产品护城河。

### Step 3.1 轨迹与数据流建模
- 为每个 session 维护 **数据来源标签 (taint)**：
  哪些内容来自 untrusted 源（外部网页 / 邮件 / 第三方 MCP 返回）。
- taint 随上下文传播：untrusted 内容影响了后续工具调用参数 → 该调用继承 untrusted 标记。

### Step 3.2 行为策略语言 (Guardrails)
- 定义可表达轨迹约束的 DSL / 规则：
  ```
  // 来自不可信源的信息不得触发高敏工具
  raise "indirect prompt injection" if:
      untrusted_source -> reaches -> tool_call(sensitivity == HIGH)
  ```
- Pre 阶段：在调用前就用已积累的轨迹判断当前调用是否落入危险链路。
- Post 阶段：深度复盘，还原完整攻击路径。

### Step 3.3 与前两轴联动
- 行为轴给出的信号进入同一个汇总决策：
  例：权限轴 ALLOW + 数据轴 ALLOW，但行为轴发现 `untrusted_email → customer_db → external_send` → 整体 BLOCK。

### Step 3.4 MCP-Scan 静态扫描
- 上线前扫描接入的 MCP Server：工具描述是否含注入、权限是否过宽、schema 是否可疑。

**交付物**：taint 传播、行为 Guardrails DSL、三轴联动决策、MCP-Scan。
**验收（招牌 Demo）**：跑通 README §3 的间接注入剧本 —— 单看三个工具都合法，
串起来被行为轴识别并拦截，根因定位到「外部邮件间接注入」。

---

## Phase 4 — 分析面闭环与运营中心（第 17–22 周）

**目标**：从「拦截」升级到「学习」。SOC 真正闭环 + 好用的运营控制台。

### Step 4.1 根因分析
- 对被 BLOCK / 可疑的轨迹，SOC（Python）做因果还原：
  输入源 → 中间步骤 → 危险动作，输出人类可读的根因说明。
- 可选：接 LLM 做轨迹摘要与解释（离线，不阻断实时链路）。

### Step 4.2 策略建议引擎
- 从重复出现的攻击模式自动生成策略草案（Cedar / Guardrails DSL）。
- 建议带：命中样本、预计影响面（会拦掉多少历史正常流量，防误杀）、置信度。

### Step 4.3 处置反馈闭环
- 管理员 `[接受 / 修改 / 拒绝]` 建议 → 策略下发 → Gateway 热加载 → 下次提前拦截。
- 记录每条策略的实战命中率，反哺建议排序。

### Step 4.4 运营控制台（TS + React）
- 实时告警流、轨迹时间线、审批中心、策略管理、误报申诉、指标看板。

**交付物**：根因分析、策略建议引擎、处置反馈闭环、Web 控制台。
**验收**：一次真实攻击从「发现 → 根因 → 建议 → 管理员接受 → 策略生效 → 复现被提前拦」全流程闭环，全程有界面。

---

## Phase 5 — 生产化与生态（第 23 周起）

**目标**：可部署、可扩展、可信任。

- **性能**：p99 决策延迟目标（Pre 轴 < 20ms 内联；重分析异步）。压测与降级策略。
- **高可用**：Gateway 无状态水平扩展；策略/身份走集中存储；fail 策略可配。
- **多租户 & RBAC**：控制台自身的权限体系、审计。
- **部署**：docker-compose（单机 demo）→ Helm/K8s（生产）→ sidecar / DaemonSet（出网拦截）。
- **可观测性**：OpenTelemetry trace 贯穿 Agent→Gateway→Tool；指标与审计导出 SIEM。
- **生态**：ToolHive / Bifrost / Pipelock / Invariant 作为可选 Engine 适配器发布；
  开放 Engine SDK 让第三方接检测能力。
- **合规**：审计不可篡改（append-only / 签名），满足 SOC2 / 等保取证需求。

---

## 里程碑总览

| 里程碑 | 阶段 | 关键能力 | 可对外说 |
|--------|------|----------|----------|
| M0 骨架 | Phase 0 | MCP Proxy 透传 + Engine 接口 | 「我们能站在 Agent 和工具中间」 |
| M1 权限闸门 | Phase 1 | 权限轴 + CONFIRM + 最小闭环 | 「Agent 调工具前的一道权限闸门」 |
| M2 出网防线 | Phase 2 | 数据/网络轴 DLP/SSRF/REDACT | 「Agent 的出网防火墙 + DLP」 |
| M3 行为大脑 | Phase 3 | 行为/因果轴 + 三轴联动 | 「识别多步攻击链，护城河」 |
| M4 安全闭环 | Phase 4 | 根因 + 策略建议 + 运营中心 | 「策略随真实行为进化的闭环」 |
| M5 生产就绪 | Phase 5 | HA / 多租户 / 生态 / 合规 | 「企业可落地的 Agent 安全控制面」 |

---

## 团队分工建议（并行）

- **Gateway/数据面 (Go)**：Phase 0 骨架 → Phase 1/2/3 三轴 Engine 主干。
- **策略/引擎**：Cedar/OPA 集成、Guardrails DSL、决策汇总算法。
- **分析面 (Python)**：轨迹还原、根因、策略建议引擎。
- **前端 (TS/React)**：审批中心 → 运营控制台。
- **平台/SRE**：CI/CD、部署形态、压测、可观测性。

先集中打通 Phase 0→1 的一条最细闭环，再横向铺三轴。

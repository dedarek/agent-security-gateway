# 架构设计 —— 三轴风险引擎

本文件详解 Agent Security Gateway 的内部架构：三轴模型、Pre/Runtime/Post 生命周期、
统一决策引擎、数据模型、汇总算法、部署形态。

---

## 1. 总体分层

```
┌──────────────────────────────────────────────────────────────┐
│                        Agent Runtimes                          │
│   Claude Code · Codex · LangGraph · AutoGen · 自研 Agent        │
└───────────────────────────┬──────────────────────────────────┘
                            │ MCP / Tool Call / HTTP egress
                            ▼
┌──────────────────────────────────────────────────────────────┐
│                   DATA PLANE  ·  Gateway (Go)                  │
│                                                                │
│   Ingress (MCP Proxy / Forward Proxy / /v1/decision)           │
│        │                                                       │
│        ▼                                                       │
│   ┌───────────────── Risk Decision Engine ─────────────────┐   │
│   │                                                        │   │
│   │   Axis A: Permission     (ToolHive-class engines)      │   │
│   │   Axis B: Data/Network   (Pipelock-class engines)      │   │
│   │   Axis C: Behavior/Causal(Invariant-class engines)     │   │
│   │                                                        │   │
│   │   Pre ─▶ Runtime ─▶ Post   (三轴各阶段并行 → 汇总)        │   │
│   └────────────────────────┬───────────────────────────────┘   │
│        │ ALLOW/BLOCK/CONFIRM/REDACT                            │
│        ▼                                                       │
│   Egress (转发到真实 MCP / Tool / Internet)                     │
│        │                                                       │
│        ▼ emit Event                                            │
└────────┼─────────────────────────────────────────────────────┘
         │ event bus (NATS/Kafka)
         ▼
┌──────────────────────────────────────────────────────────────┐
│           ANALYSIS PLANE · Intelligence / SOC (Python)         │
│   轨迹还原 → 根因分析 → 攻击模式挖掘 → 策略建议                    │
└───────────────────────────┬──────────────────────────────────┘
                            │ policy (Cedar / Guardrails DSL)
                            └──────────▶ 回写 Gateway (热加载)
```

---

## 2. 三轴模型（正交，不是先后）

**核心纠正**：ToolHive ≠「调用前」，Pipelock ≠「调用后」。三者是三条正交轴，
每条轴都在 Pre / Runtime / Post 三个阶段有动作。

### Axis A — 权限轴（Permission）· ToolHive 类
- **管什么**：身份、工具准入、资源边界、参数越权。
- Pre：principal(agent/user) 能否对 resource 执行 action？参数是否越权？
- Runtime：在最小权限沙箱内执行；能力裁剪。
- Post：结果是否超出授权范围？是否发生权限扩张？留审计证据。

### Axis B — 数据/网络轴（Data & Network）· Pipelock 类
- **管什么**：出网目标、SSRF、DLP/密钥外泄、内容投毒。
- Pre：目标域名/URL 是否允许？参数是否携带敏感数据？是否 SSRF？
- Runtime：出网流量当场拦截；流式内容逐块扫描。
- Post：返回内容是否含敏感信息 / Prompt Injection / Tool Poisoning？数据最终流向？

### Axis C — 行为/因果轴（Behavior & Causal）· Invariant 类
- **管什么**：多步轨迹、间接注入、行为链、数据流因果。
- Pre：已积累的轨迹是否已构成危险链路（即使当前单步合法）？
- Runtime：taint 传播 —— 不可信来源如何影响后续调用。
- Post：还原完整攻击路径，生成根因与策略建议。

---

## 3. Pre / Runtime / Post 生命周期

```
        ToolCall 进入
             │
     ┌───────▼────────┐
     │   PRE 评估      │  三轴并行 EvaluatePre
     │  A ∥ B ∥ C      │
     └───────┬────────┘
             ▼
        汇总决策 #1 ── BLOCK ─▶ 拒绝 + emit Event
             │  CONFIRM ─▶ 挂起审批队列 ─(approve)─┐
             │  REDACT  ─▶ 改写参数                │
             ▼ ALLOW / (approved)                  │
     ┌───────▼────────┐ ◀───────────────────────────┘
     │  RUNTIME 执行   │  转发到真实工具；三轴 EvaluateRuntime
     │  (流式检查)      │  出网/流内容逐块；taint 传播
     └───────┬────────┘
             ▼
     ┌───────▼────────┐
     │   POST 评估     │  三轴并行 EvaluatePost（对 ToolResult）
     │  A ∥ B ∥ C      │
     └───────┬────────┘
             ▼
        汇总决策 #2 ── BLOCK/REDACT 返回内容
             │
             ▼ emit Event(trajectory 沉淀) ─▶ SOC
        返回给 Agent
```

---

## 4. 核心数据模型（proto，Go/Python 共用）

```proto
message ToolCall {
  string call_id      = 1;
  Principal principal = 2;   // agent + user + session
  string tool_id      = 3;   // e.g. "database.delete_user"
  string resource     = 4;   // e.g. "database.users"
  string action       = 5;   // read/write/delete/network...
  bytes  arguments    = 6;   // JSON
  repeated Taint taints = 7; // 数据来源标签(可信/不可信)
  int64  ts           = 8;
}

message ToolResult {
  string call_id = 1;
  bytes  output  = 2;
  bool   error   = 3;
  repeated Taint result_taints = 4;
}

message Signal {          // 单个 Engine 的输出
  Axis   axis      = 1;   // PERMISSION | DATA_NETWORK | BEHAVIOR
  string engine    = 2;
  int32  score     = 3;   // 0-100 风险分
  Verdict verdict  = 4;   // ALLOW|BLOCK|CONFIRM|REDACT
  repeated string reasons  = 5;
  repeated Evidence evidence = 6;
  FailMode fail_mode = 7;  // FAIL_OPEN | FAIL_CLOSED
  repeated Redaction redactions = 8;
}

message Decision {
  string call_id = 1;
  Verdict final  = 2;
  repeated Signal signals = 3;
  string phase   = 4;    // PRE | RUNTIME | POST
  string rationale = 5;
}

message Event {          // 沉淀给 SOC
  Decision decision = 1;
  ToolCall call     = 2;
  ToolResult result = 3;
  string session_id = 4;
  int64  ts         = 5;
}
```

---

## 5. 决策汇总算法（多轴 → 单一出口）

多个 `Signal` 汇总成一个 `Verdict`，默认规则（可按策略覆盖）：

```
输入: signals[]  (来自三轴、各阶段)

1. 任一 signal.verdict == BLOCK              → BLOCK
2. 否则 任一 signal.verdict == CONFIRM        → CONFIRM   (人在环路)
3. 否则 存在 signal.verdict == REDACT         → REDACT    (合并所有 redactions)
4. 否则                                       → ALLOW

风险分聚合(用于告警/排序，不直接决定 verdict):
   risk = max(scores)  且  记录 sum 用于多低危叠加告警

Fail 处理:
   Engine 报错时按其 fail_mode 处理：
   - FAIL_CLOSED 引擎报错 → 视为 BLOCK（高敏工具默认 fail-closed）
   - FAIL_OPEN   引擎报错 → 视为 ALLOW + 记录降级告警（低敏路径）
```

> 关键：BLOCK 一票否决保证安全；CONFIRM 让人兜底；REDACT 兼顾可用性。

---

## 6. Engine 插件模型

所有检测能力（含 ToolHive/Pipelock/Invariant 适配器）实现统一接口，Gateway 只认接口：

```go
type Axis int
const ( AxisPermission Axis = iota; AxisDataNetwork; AxisBehavior )

type Engine interface {
    Name() string
    Axis() Axis
    EvaluatePre(ctx context.Context, c *ToolCall) (*Signal, error)
    EvaluateRuntime(ctx context.Context, c *ToolCall, s *Stream) (*Signal, error)
    EvaluatePost(ctx context.Context, c *ToolCall, r *ToolResult) (*Signal, error)
}
```

- 注册表按 Axis 分组管理，配置文件启停。
- 内置 Engine（自研）+ 外部 Engine（gRPC sidecar，跨语言，如 Python 行为分析）。
- 这样 **架构不绑死任一开源项目**：ToolHive 换 Bifrost 只是换一个权限轴 Engine。

---

## 7. 部署形态

| 形态 | 用途 | 拦截点 |
|------|------|--------|
| MCP Proxy | 主形态，Agent 连 Gateway 虚拟 MCP | 工具调用参数/返回 |
| Forward Proxy (MITM) | 管住 Agent 任意出网 HTTP(S) | 出网流量（数据/网络轴） |
| Sidecar + /v1/decision | 自研 Agent 主动送审 | 应用内埋点 |
| K8s DaemonSet + eBPF | 集群级出网可见性 | 网络层 |

生产：Gateway 无状态水平扩展；策略/身份/事件走集中存储；SOC 与控制台独立部署。

---

## 8. 闭环数据流

```
Gateway ──Event──▶ NATS ──▶ SOC(轨迹/根因/建议) ──策略草案──▶ 控制台
                                                              │
                                          管理员 接受/修改/拒绝 │
                                                              ▼
                                          Policy Store (Cedar/DSL)
                                                              │
                                                    热加载 ◀──┘
                                                       │
                                                       ▼
                                                    Gateway (下次提前拦截)
```

每条策略记录实战命中率，反哺 SOC 的建议排序与误报控制。

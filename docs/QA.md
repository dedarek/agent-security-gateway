# ASG 安全网关 — 技术 QA

---

## Q1: 被 BLOCK 后，agent 这边会返回什么？网关如何记录？怎么看到？

### Agent 收到什么

Agent 收到的是一条 **MCP 协议标准的 tool result**，`isError: true`，内容包含明确的拒绝理由：

```json
{
  "jsonrpc": "2.0",
  "id": 30,
  "result": {
    "content": [{
      "type": "text",
      "text": "BLOCKED by Agent Security Gateway: permission: cedar denied call_tool on gw.delete_user for role=employee"
    }],
    "isError": true
  }
}
```

关键点：
- Agent 不需要特殊处理——它就是一个正常的工具调用失败。模型会读到拒绝理由并自行调整行为
- 理由是**人可读的**（"cedar denied... role=employee"），不是模糊的报错码
- 如果是 CONFIRM 被拒，理由是 `human denied confirmation`；超时未批则是 `approval timeout`

### 网关如何记录

每个决策（无论 ALLOW/BLOCK/REDACT/CONFIRM）都会生成两条持久化记录：

1. **事件条目**：写入 JSONL 事件库（含 trace_id 因果链、完整参数、判定理由）
2. **签名回执**：Ed25519 签名的 ActionReceipt（防篡改，可事后验证链完整性）

### 怎么看到

| 方式 | 地址/命令 |
|---|---|
| 控制台轨迹回放 | 打开 `:8090` → 左侧选 session → 右侧逐帧时间线（彩色标注 BLOCK/REDACT） |
| 根因分析 | 同页面下方，自动还原因果链 |
| SIEM 导出 | `GET /api/siem?format=cef` 或 `format=splunk` |
| API 查询 | `GET /api/events` 或 `GET /api/trajectory?session=xxx` |
| 签名验证 | `receipt.VerifyChain(receipts, pubKey)` → 返回 true/false |

---

## Q2: 安全防护现在都防护哪些类型？如何编排？

### 三轴 × 四处置 = 防护矩阵

| 轴 | 管什么 | Pre(调用前) | Post(结果后) |
|---|---|---|---|
| **权限轴** | 你有没有资格做这件事 | Cedar 策略判定角色→工具映射 | 结果是否超出授权范围 |
| **数据/网络轴** | 数据往哪流、内容安不安全 | 参数 DLP 扫描 + 目标域名检查 | 返回内容密钥/PII/注入扫描 + 流式分块扫描 |
| **行为/因果轴** | 几步连起来是不是攻击 | taint 数据流追踪 + Invariant DSL 规则 | — (Pre 已覆盖) |

### 编排模型

一次工具调用，三轴**并行**评估（非串行），汇总成统一风险决策：

```
         ┌── 权限轴(Cedar) ──┐
请求 ──► ├── 数据轴(Pipelock) ├──► Risk Decision Engine ──► 最终裁决
         └── 行为轴(taint+DSL)┘
```

聚合规则（一票否决制）：
- 任一引擎说 BLOCK → 最终 BLOCK
- 否则任一说 CONFIRM → 挂起等人批
- 否则任一说 REDACT → 打码后放行
- 全部 ALLOW → 放行

三轴贯穿 Pre/Runtime/Post 三个生命周期阶段，只是侧重不同：

| 阶段 | 权限轴侧重 | 数据轴侧重 | 行为轴侧重 |
|---|---|---|---|
| Pre | 能不能调这个工具 | 参数有没有敏感数据 | 上游是否为不可信源 |
| Runtime | — | 流式响应逐块扫描 | — |
| Post | 结果有无越权 | 结果有无密钥泄漏 | — |

---

## Q3: 每种防护类型的具体技术实现

### 权限轴 — Cedar 策略引擎

- **引擎**: cedar-go v1.8.0（AWS 开源，ToolHive 同款）
- **策略语言**: Cedar DSL（声明式，人类可读写）

```cedar
// 示例: employee 不能删用户
forbid (
  principal, action == Action::"call_tool",
  resource == Tool::"gw.delete_user"
) when { principal.role == "employee" };

// 示例: 导出操作需要人工确认(auto_execute deny => CONFIRM)
forbid (
  principal, action == Action::"auto_execute",
  resource == Tool::"gw.export_all_users"
);
```

- **双动作模型**: `call_tool`（能不能跑）+ `auto_execute`（能不能自动跑）。deny 前者→BLOCK，deny 后者→CONFIRM
- **策略热更新**: Intelligence 分析后一键下发新策略，网关不重启即生效；解析失败保留旧规则

### 数据/网络轴 — Pipelock 规则包

- **规则来源**: Pipelock 社区规则包（423 条规则，Ed25519 签名验签加载）
- **三种规则类型**:
  - `dlp`: 密钥/PII 正则匹配（1Password token、AWS key、邮箱等）→ REDACT
  - `injection`: prompt-injection 话术匹配 → BLOCK
  - `tool-poison`: 工具名仿冒系统二进制 → BLOCK
- **扫描范围**: 工具调用参数（Pre）+ 工具返回结果（Post）+ 流式响应（Runtime 分块）
- **REDACT 实现**: DLP 引擎用 `FindAllStringIndex` 定位命中区间 → proxy 层用 `strings.ReplaceAll` 实际改写字节 → agent 收到的就是打码后的内容

### 行为/因果轴 — 双引擎

**引擎A: 内容级 taint 传播（自研 Go）**

```
get_inbox 返回邮件内容
    ↓ 提取高信号 token（邮箱/URL/域名）
    标记为 TaintMark{source:"get_inbox", tokens:[attacker@gmail.com]}
    
后续 send_email(to="attacker@gmail.com")
    ↓ Pass1: 参数值与 taint token 匹配 → 命中!
    BLOCK: "value originated from untrusted source get_inbox"
```

精度保证：短值(<6字符)不参与匹配，防止 `"com"` 误触所有域名。

**引擎B: Invariant DSL sidecar（Python）**

- 引擎: invariant-ai 0.3.5 (`Policy.from_string`)
- 策略语法:
```
raise PolicyViolation("email exfil") if:
    (call: ToolCall)
    call is tool:send_email
```
- 通过 HTTP sidecar 调用，Go 发轨迹 JSON → Python 分析 → 返回违规列表
- failMode 可配：FailClosed（sidecar挂=全BLOCK）或 FailOpen（降级可用）

---

## Q4: 用血缘的思想追溯威胁链路——实现了吗？怎么实现？如何查看？

### 实现状态：✅ 已实现，三层追溯能力

### 第一层：trace_id 因果链（已上线）

每个探针上报的事件都带两个 ID：

```
trace_id: "trace-tenant-alice-0823-143000"   ← 同一次任务的所有事件共享
parent_id: "llm-1724424000"                   ← 指向触发本次调用的 LLM 请求
```

这样事后可以回答："这条被拦的 send_email 是哪次 LLM 对话触发的？"——顺着 parent_id 往上找就行。

### 第二层：根因分析（已上线，自动生成）

Intelligence 面自动分析 session 内的完整轨迹，输出因果链报告：

```
根因: indirect prompt injection / data-flow: 
      value "attacker@gmail.com" first appeared in gw.get_inbox output, 
      then flowed into blocked call gw.send_email

攻击链: untrusted inbox content flowed into egress
```

同时自动起草一条 Cedar 策略建议，管理员点一下就能热下发。

### 第三层：Semantica 血缘图谱（已上线，交互式可视化）

把事件流构建成 W3C PROV-O 兼容的实体关系图：

```
Agent(alice) --performed--> Event(BLOCK) --used--> Tool(send_email)
Event(BLOCK) --targeted--> ExternalActor(attacker@gmail.com)
Trace(trace-xxx) --includes--> Event(BLOCK)
```

**如何查看**：控制台点击「🌐打开交互式图谱」→ 进入 Semantica Explorer：

- **图布交互**: 节点可拖拽，沿边追溯上下游，点节点看属性
- **语义检索**: 输入 "email exfiltration to attacker"，本地 embedding 返回相似历史事件排名
- **KG-grounded 问答**: 直接用自然语言问 "Was there an exfiltration attempt?" → 图谱上下文 + 免费模型生成解释性回答
- **决策溯源**: 每个 BLOCK 的 causal chain、先例查询、合规检查
- **时序回放**: 按时间轴滑动看事件演化过程

### 底层技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 事件采集 | Go gateway + asg-connect probe | 全量捕获，JSONL 落盘 |
| 因果标记 | trace_id + parent_id | 探针维护，跨 LLM/tool/shell 统一 |
| 血缘存储 | Semantica ProvenanceManager | W3C PROV-O 标准，SQLite 持久化 |
| 图谱建模 | Semantica KnowledgeGraph | 内存实体关系图 |
| 语义检索 | fastembed (本地 ONNX bge-small) | 384 维向量，零 API 费 |
| 图谱问答 | Semantica OpenAI wrapper → 探针 → ox-alpha-free | 免费 |
| 防篡改 | Ed25519 哈希链签名回执 | 事后可验证完整性 |
| SIEM 对接 | CEF / Splunk HEC 格式导出 | `/api/siem?format=` |

---

## 总结一句话

> **BLOCK 了 agent 知道为什么；网关记了签名回执不怕抵赖；控制台能看到彩色轨迹和自动生成的根因；Explorer 里能在交互式血缘图上拖拽溯源；还能直接问图谱"发生了什么"得到自然语言解释；最后全部可导出给企业 SIEM 归档。**

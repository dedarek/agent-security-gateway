# 开源选型分析 —— fork 谁 / 自研什么

结论先行，再展开四个项目的定位、可复用点、以及我们的取舍。

---

## 0. 结论（TL;DR）

**自研 Gateway 主干，把四个开源项目的能力做成可插拔 Engine/Adapter，不 fork 任何一个当底座。**

理由：产品是「统一安全控制面 + 分析闭环」，任何单一开源项目都只覆盖三轴中的一条。
若 fork 其一当底座，架构会被它的抽象绑死，另外两轴变成「打补丁」。
反之，自研一层薄主干（Ingress + Risk Decision Engine + Engine 注册表 + 事件闭环），
让每个开源项目在自己擅长的轴上作为 Engine 接入，可替换、可组合。

| 项目 | 本质 | 在我们产品里的角色 | 复用方式 |
|------|------|--------------------|----------|
| ToolHive | MCP 安全运行时 / 管理层 | **权限轴** Engine + MCP 运行/隔离底座 | 借鉴 MCP 代理 + 隔离运行 + Cedar 授权 |
| Bifrost | AI + MCP Gateway | 可选的**模型/MCP 入口** Engine | 需要模型路由时接入，非必须 |
| Pipelock | Agent 出网防火墙 | **数据/网络轴** Engine | 拆其出网/DLP/SSRF 能力 |
| Invariant | Agent 行为策略/轨迹分析 | **行为/因果轴** 思想来源 | 借 Guardrails / MCP-Scan / trajectory 思路，不当底座 |

---

## 1. ToolHive —— 最像安全执行基座

- **是什么**：MCP 的 Zero-Trust Runtime。把一堆 MCP Server（filesystem/github/db/slack）
  管起来，做隔离运行、身份认证、工具白名单、Cedar 权限控制、审计。
- **强在**：MCP 运行时 + 权限闸门（「Agent 调工具之前的一道权限闸门」）。Go 实现，贴合我们主干。
- **在我们产品里**：权限轴（Axis A）的主力 Engine，并可复用其 MCP 隔离运行能力做部署底座。
- **二开判断**：可 **借鉴其 MCP proxy + 隔离模型**，但权限决策统一收敛到我们的 Risk Decision Engine，
  不让 ToolHive 的策略成为唯一出口。

## 2. Bifrost —— 最像通用 Gateway 基座

- **是什么**：先是 LLM Gateway（模型路由/统一 API/负载/fallback/成本），后加 MCP Gateway
  （工具 allowlist / Virtual Key / 自动执行 vs 审批）。「企业 AI 的总网关」。
- **强在**：模型侧治理 + 通用网关能力。范围比 ToolHive 大。
- **在我们产品里**：**可选**。若客户还要「统一模型入口 + 成本/路由」，Bifrost 作为上游模型网关接入；
  但我们的安全价值在工具/行为轴，不需要用 Bifrost 当底座。
- **二开判断**：不 fork。作为「模型入口」旁路集成，避免把安全产品做成通用 AI 网关。

## 3. Pipelock —— 出网安全能力可直接拆

- **是什么**：Agent 出网防火墙。放在 `Agent → Pipelock → Internet`，所有出网流量过它，
  做 SSRF、恶意域名、Secret/DLP、Prompt Injection、Tool Poisoning 检测。
  「Proxy + WAF + DLP + Egress Firewall，只是对象变成 Agent」。
- **强在**：数据/网络轴（Axis B）几乎现成。
- **在我们产品里**：数据/网络轴 Engine 的主力，直接吸收其 forward-proxy + DLP/SSRF 检测。
- **二开判断**：**很适合拆能力**。把它的出网检测封装成我们的 Axis B Engine（内联或 sidecar）。

## 4. Invariant —— 借行为链思想，不当底座

- **是什么**：Agent 行为策略和轨迹分析。擅长表达「来自不可信网页/邮件的信息，
  后续触发了高风险工具调用则阻止」。策略语言 / Guardrails / MCP-Scan 都围绕 trajectory / data flow。
  「Agent 的行为检测引擎」。
- **强在**：行为/因果轴（Axis C）—— 产品护城河所在。
- **在我们产品里**：Axis C 的思想与规则语言来源；taint 传播、Guardrails DSL、MCP-Scan 借鉴它。
- **二开判断**：**不适合当整个平台底座**（它是分析引擎不是网关）。作为 Engine + 分析面能力接入。

---

## 2. 为什么「自研主干 + Engine 化」而不是 fork 其一

```
若 fork ToolHive 当底座:
   权限轴 ✓  → 但 数据/网络轴、行为轴 都要塞进它的抽象，别扭
若 fork Bifrost 当底座:
   通用网关 ✓ → 安全变成插件，产品定位漂移成"AI 网关"
若 fork Pipelock 当底座:
   数据轴 ✓  → 权限/行为轴缺失，且它是 egress 视角不是工具治理视角
若 fork Invariant 当底座:
   行为轴 ✓  → 它是分析引擎，不具备实时网关主干

自研薄主干:
   Ingress + Risk Decision Engine + Engine 注册表 + 事件闭环
   → 三轴都是一等公民，四个开源项目各归其位，可换可组合  ✅
```

---

## 3. 落地对应关系

| 我们的模块 | 主要借力 | 备注 |
|------------|----------|------|
| MCP Proxy / 隔离运行 | ToolHive | 部署底座可复用 |
| 权限轴 Engine + Cedar | ToolHive | 决策收敛到我方引擎 |
| 数据/网络轴 Engine | Pipelock | 出网/DLP/SSRF 直接拆 |
| 行为/因果轴 Engine | Invariant | Guardrails/MCP-Scan/taint 思路 |
| 可选模型入口 | Bifrost | 需要模型治理时旁路接入 |
| Risk Decision Engine / 闭环 | 自研 | 产品核心，不外包 |

> 一句话：**四个开源项目是四把好刀，我们造的是握刀的手 + 决策的脑。**

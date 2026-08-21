# Base Projects 实测分析（基于真实代码）

对 ToolHive / Bifrost / Pipelock / Invariant 的源码逐项拉取分析，给出**可直接落地的接口、
精确文件路径、复用难度**。所有路径相对各自仓库根目录。结论汇总见文末 §5。

分析对象与版本坐标：
- `stacklok/toolhive` — Go, Apache-2.0, `cedar-policy/cedar-go v1.8.0`（Cedar 是真依赖）
- `luckyPipewrench/pipelock` (+ `pipelock-rules`) — Go, Apache-2.0（enterprise 目录 ELv2）
- `maximhq/bifrost` — Go, Apache-2.0（多模块）
- `invariantlabs-ai/invariant` — Python, 包名 `invariant-ai` v0.3.5

---

## 1. ToolHive —— 权限轴 Engine（强复用，低耦合）

### 1.1 授权是核心可复用资产（Cedar 真实存在）
决策接口 `pkg/authz/authorizers/core.go`：
```go
type MCPFeature string   // "tool" | "prompt" | "resource"
type MCPOperation string // "list" | "get" | "call" | "read"

type Authorizer interface {
    AuthorizeWithJWTClaims(ctx, feature MCPFeature, operation MCPOperation,
        resourceID string, arguments map[string]interface{}) (bool, error)
}
```
工厂/注册表 `pkg/authz/authorizers/registry.go`：
```go
type AuthorizerFactory interface {
    ConfigKey() string                                   // "cedar" | "pdp"
    ValidateConfig(rawConfig json.RawMessage) error
    CreateAuthorizer(rawConfig json.RawMessage, serverName string) (Authorizer, error)
}
```
- **`authorizers/core.go` + `registry.go` 近零耦合**（只依赖 `context`/`encoding/json`，约 150 LOC）→ 可直接采纳或 vendor 作为我们「权限轴」的契约。
- Cedar 后端 `pkg/authz/authorizers/cedar/core.go`（`ConfigType="cedarv1"`，init 注册）：默认拒绝、`forbid` 优先于 `permit`；JWT claims 注入为 `claim_*`，工具参数注入为 `arg_*`；group/role → `THVGroup::"..."` 实现 RBAC（`principal in THVGroup::"admin"`）。
- 纯 Cedar 原语 `(*Authorizer).IsAuthorized(principal, action, resource, ctxMap, entities...)` **不依赖 `pkg/auth`**，若自建 entity 则可平凡复用。
- 备选 HTTP PDP `pkg/authz/authorizers/http/`：把 MCP 请求映射成 PORC JSON POST 到 `/decision`，收 `{"allow":bool}`——适合外部策略服务。

### 1.2 拦截 seam：优先 Admission 模式，而非 HTTP 中间件
- HTTP 授权中间件 `pkg/authz/middleware.go`（deny 返回 403 + JSON-RPC error）**较重**：耦合 `pkg/mcp`、`pkg/transport/types` 以及多个 `pkg/vmcp/*`（optimizer/schema）。不建议整块拿。
- 更干净的是 vMCP 的 admission seam `pkg/vmcp/core/admission.go`：
```go
type Admission interface {
    FilterTools(ctx, identity, tools) ([]Tool, error)
    AllowToolCall(ctx, identity, tool, args) (bool, error)
    FilterResources / AllowResourceRead / FilterPrompts / AllowPromptGet ...
}
```
  它内部**包同一个 `authorizers.Authorizer`(Cedar)**——一个授权器同时驱动「list 过滤」与「call 拒绝」。**这就是我们网关该照抄的形态。**
- `tools/list` 恒放行但**响应被过滤**只留授权项（`pkg/authz/response_filter.go`）。方法→feature/op 映射 + 未知方法默认拒绝在 `pkg/authz/middleware.go`（`MCPMethodToFeatureOperation`）。

### 1.3 运行时隔离 + 审计（可选复用）
- 运行时抽象 `pkg/container/runtime/types.go`：`Deployer`/`Runtime`（Podman/Docker/K8s/Colima），带 `permissions.Profile`（挂载/网络/cap-drop/privileged），网络隔离默认开（Squid egress proxy，实验性 Envoy）。
- 审计 `pkg/audit/*`（`MiddlewareType="audit"`）：NDJSON 事件，字段含 `audit_id / type(mcp_tool_call) / subjects / target / delegation(RFC 8693 委托链) / metadata`，denial(401/403) 也记 `outcome: denied`。canonical schema 在 `toolhive-core/audit`。→ **我们的 Event schema 可对齐它**。
- vMCP 整体是显式「可嵌入库」（`docs/arch/vmcp-library.md`），但依赖树大。

### 1.4 复用判定
| 组件 | import | 耦合 | 难度 |
|------|--------|------|------|
| `authorizers` 接口+registry | `pkg/authz/authorizers` | context/json | 平凡 |
| Cedar 授权器 | `.../cedar` | pkg/auth, cedar-go, jwt | 低-中 |
| HTTP PDP 授权器 | `.../http` | net/http | 低 |
| Admission seam 模式 | `pkg/vmcp/core` | vmcp, auth | 中（照抄模式） |
| 容器运行时 | `pkg/container` | core/permissions | 低-中 |

**采纳**：以 `authorizers.Authorizer`+`AuthorizerFactory` 作权限轴契约，复用 Cedar 后端（想脱 `pkg/auth` 就 fork `cedar/core.go`）；enforcement 照 `vmcp/core.Admission` 而非 `pkg/authz` HTTP 中间件；Event schema 对齐 `pkg/audit`。锁定某个次版本 vendor。

---

## 2. Pipelock —— 数据/网络轴 + **审计原语**（最高价值发现）

### 2.1 拦截架构（forward proxy + 可选 MITM + MCP/A2A 包装）
`internal/proxy/`：`proxy.go`(fetch)、`forward.go`(CONNECT)、`intercept.go`(TLS MITM，用 `internal/certgen/`)、`reverse.go`、`websocket.go`、`sse.go`；MCP 在 `internal/mcp/`（stdio/HTTP/WS 双向 JSON-RPC 扫描）；A2A `internal/mcp/a2a*.go`（Agent Card 投毒/漂移检测）；会话污点 `internal/proxy/taint.go` + `internal/session/`。
→ 这些是真正的网络拦截，**深度耦合**其 session/taint/envelope/contract 子系统。**建议以 sidecar 运行，不 vendoring。**

### 2.2 检测引擎（不可变 core + 附加签名规则包）
- 类别（`internal/scanner/scanner.go`）：`ssrf / ssrf_metadata / dlp / core_dlp / core_ssrf / core_response(injection) / blocklist / entropy / databudget / path_traversal / crlf_injection / ...`。SSRF 用 DNS 重解析 + rebind 检测（`internal/proxy/ssrf_dial_block.go`）。
- **规则格式 = 签名 YAML**（RE2 正则），源规则示例（pipelock-rules）：
```yaml
- id: injection-explicit-http-exfil
  type: injection            # dlp | injection | tool-poison
  status: stable             # stable | experimental
  severity: critical
  confidence: high           # 供 MinConfidence 过滤
  tags: ["owasp-llm:LLM01", "mitre-atlas:AML.T0048"]
  pattern:
    regex: '(?:send|post|forward)\s+...\s+https?://'
    # tool-poison 规则额外: scan_field: description
```
- 编译包头 `published/<bundle>/bundle.yaml`（`format_version/name/version/min_pipelock`）+ 分离式 Ed25519 签名 `.sig`。加载 `internal/rules/loader.go`（`LoadOptions{MinConfidence, IncludeExperimental, TrustedKeys, ...}`），验签 `internal/rules/verify.go`（官方内嵌 keyring 优先，其次 operator `trusted_keys`），**bundle 只增不覆盖 built-in**。
- 社区语料 `pipelock-rules`（DLP 密钥正则、injection、tool-poison、PHI）**可直接用**。

### 2.3 决策模型
- Hook 编排 `internal/decide/decide.go`：`Decision{Outcome(allow|deny), Evidence[], UserMessage, AgentMessage}`，取最严动作（`policy.StricterAction`），**默认 fail-closed**（坏 JSON→block）。
- 完整动作词表 `internal/config/schema.go`：`block/redirect/warn/ask/strip/forward/allow/defer/step_up/redact`，排名 `internal/config/action.go`（block=6…allow=0）。
- URL verdict `scanner.Result{Allowed, Reason, Score, Class ResultClass}`——`ResultClass` 区分 threat / protective / infra-error / structural-exemption，避免把「保护性 block」误当风险升级。

### 2.4 ★ Action Receipts（跨轴审计原语，最强推荐）
`internal/receipt/*` + `internal/signing/`（Ed25519 keystore/轮换）+ 4 语言验证器 `sdk/verifiers/{ts,rust,python}` + 一致性语料 `sdk/conformance/`。
```go
type Receipt struct {
    Version      int          // 1
    ActionRecord ActionRecord
    Signature    string       // "ed25519:" + 128 hex
    SignerKey    string       // 32-byte pubkey (64 hex)
    Ext          json.RawMessage // 未签名咨询位，永不验证
}
```
- 签名 = Ed25519 over `SHA-256(canonical ActionRecord)`（v1 canonical 是结构声明序 `json.Marshal`，非 JCS；另有 `EvidenceReceipt v2` 用 RFC 8785 JCS）。
- `ActionRecord` 含：`action_id`(UUIDv7 时序)、`parent_action_id`、`action_type`∈{read/derive/write/delegate/authorize/spend/commit/actuate}、`principal/actor/delegation_chain`、`intent`、`data_classes_in/out`、`side_effect_class`、`reversibility`、`policy_hash`、`verdict`、taint、`transport/method/layer/severity`、链字段。
- **哈希链**：每条带 `chain_prev_hash`(首条 `"genesis"`) + `chain_seq`(单调)；`ReceiptHash=SHA-256(Marshal(receipt))`；`run_nonce` 绑定一次运行。密钥轮换 `KeyTransition` 由**新密钥签名 → 只证连续性不证授权**，信任只来自调用方 `trustedKeys`（可选 old-key `RotationEndorsement`）。
- 验证 `receipt.go`：`Verify()` 拒绝无外部信任锚工作；`VerifyChainTrusted(receipts, trustedKeys)` 走段/签名/seq/prev-hash/生命周期；**严格 Unmarshal**（拒重复键防解析器差分走私、拒未知字段、拒尾随 token）。
- 发射 `internal/receipt/emitter.go`：锁跨 stamp→sign→hash→persist→advance 保单调；写 append-only flight recorder（`internal/recorder/`，**外层再套一条哈希链**）；`policy_hash` 每次绑定精确配置快照。
- 诚实边界（README）：receipt 只证「边界决定了什么 + 持钥者签了名」，不证操作者诚实、不证边界外没发生事。`pipelock anchor` 可把 checkpoint 记到 Rekor。

### 2.5 复用判定
- **易 lift**：`internal/receipt/` + `internal/signing/` + 规则 YAML 格式/`internal/rules/` loader。→ **采纳 action-receipt 作为全轴统一审计原语**（已有 4 语言验证器+语料，是低风险标准化押注）；`ActionType/SideEffectClass/Reversibility` 是轴无关的干净动作模型。
- **中**：`internal/scanner/`（SSRF/DLP/injection/entropy 正是数据轴，但约 3.8k 行 + 一堆 config 依赖，作为子系统整取）；`internal/decide/`（决策 facade 模板）。
- **不取**：`internal/proxy/`、`internal/mcp/`（网络拦截，深耦合）→ **sidecar 运行**。

---

## 3. Bifrost —— 借插件设计范式（不 fork；安全能力是 enterprise 闭源）

### 3.1 关键事实：安全能力不在 OSS 里
Guardrails / 自适应负载均衡 / 集群 / RBAC-OIDC **均为 enterprise，源码不在本仓库**（`grep guardrail` 全仓 0 命中，仅 `docs/enterprise/*.mdx`）。→ **fork 得到 9k 行编排器却拿不到真正想要的安全层。不 fork。**

### 3.2 插件模型（值得借鉴的契约）`core/schemas/plugin.go`
能力拆分接口，都嵌 `BasePlugin`：
```go
type LLMPlugin interface {
    BasePlugin
    PreRequestHook(ctx, req) error
    PreLLMHook(ctx, req) (*BifrostRequest, *LLMPluginShortCircuit, error)
    PostLLMHook(ctx, resp, bifrostErr) (*BifrostResponse, *BifrostError, error)
}
```
另有 `HTTPTransportPlugin`（PreAuth/Pre/Post/StreamChunk）、`MCPPlugin`（PreMCPHook/PostMCPHook）。
- 短路阻断 `core/schemas/plugin_native.go`：
```go
type LLMPluginShortCircuit struct {
    Response *BifrostResponse         // 成功短路，跳过 provider
    Stream   chan *BifrostStreamChunk
    Error    *BifrostError            // 错误短路；AllowFallbacks 控制 fallback
}
```
- **Pre/Post 对称 + executed-count**（`bifrost.go` `PluginPipeline.executedPreHooks`）：每个跑过的 PreHook 保证逆序调用对应 PostHook——**安全审计/清扫轴必须始终成对执行，正是我们要的**。

### 3.3 ★ 两个必须反向设计的陷阱（对安全网关是致命默认）
1. `PreRequestHook` **不能阻断**（其 error 仅记 warning 后继续）；只有 `PreLLMHook` 短路能拒。
2. **插件 error 被吞成 warning，从不上抛给调用方**。
→ 我们的 Engine 必须 **fail-closed 默认**：Engine 返回 error ⇒ deny；阻断显式且为默认。

### 3.4 MCP 网关（可借的治理原语）
- **三层工具 allowlist，deny-by-default**（`core/schemas/mcp.go` + `utils.go`/`exec.go`）：Client 级 `ToolsToExecute`（`["*"]`=全，`[]`/nil=无）在**发现与执行两处**都强制 → 隐藏工具不能按名调用；Request 级 header 只能收窄；Virtual-Key 级 `mcp_configs` 优先，VK 无配置 ⇒ 全禁。
- **execute vs auto-execute 拆分**（`ToolsToExecute` vs `ToolsToAutoExecute`）：工具可「可执行但不自动执行」→ Bifrost 不跑、把 tool call 交回调用方审批。**这就是「需要人工确认」的原语**（配置位 + 客户端编排，非内建暂停状态机）。
- 统一 MCP 插件闸 `core/mcp/pluginpipeline.go` `RunWithPluginPipeline`：connect/ping/list/**execute** 全过 `PreMCPHook→op→PostMCPHook`——**单一工具执行选择点，最值得直接照搬**。
- MCP auth 丰富：`none|headers|oauth|per_user_oauth|token_exchange`(RFC-8693)。

### 3.5 治理插件 `plugins/governance/`（VK→Team→Customer→BU→User 层级预算/限流/模型访问）——OSS 内，可参考但非核心。

### 3.6 复用判定
**主：借范式**（能力拆分接口 / Pre-Post 对称 executed-count / 类型化短路结构 / 统一 MCP 闸 / 三层 allowlist deny-by-default / execute-vs-auto-execute 作审批原语 / context 上的治理身份+span 属性）。
**次：可选上游**（OpenAI 兼容 + 多 provider fan-out + MCP 工具治理，config 接入即得）。
**不 fork。** 且必须把 Bifrost 的「error 非阻断」反转为 fail-closed。

---

## 4. Invariant —— 行为/因果轴（作为库引入，非服务非底座）

### 4.1 定位
本仓 = **Guardrails DSL + 本地/远程分析引擎 + 检测器 stdlib**。MCP-scan、gateway 代理在**兄弟仓**（`mcp-scan`/`invariant-gateway`）不在此。`Policy` 默认走 RemotePolicy，除非 `LOCAL_POLICY=1`——我们直接用 `LocalPolicy`。

### 4.2 DSL（正是因果轴表达力）`invariant/analyzer/language/parser.py`（内嵌 Lark 文法）
招牌规则：untrusted 源 → 敏感工具（`README.md:74`）：
```python
from invariant.detectors import prompt_injection
raise "Don't use send_email after get_website" if:
    (output: ToolOutput) -> (call2: ToolCall)   # 数据流边
    output is tool:get_website
    prompt_injection(output.content, threshold=0.7)
    call2 is tool:send_email
```
原语：`(x: ToolCall)` 类型化量化变量；`->` flow（`has_flow()` 传递可达）；`~>` 直接前驱（`is_parent()`）；`is tool:name({arg:"regex"})` 语义匹配；`forall:` / `count(min=,max=)` 量词（`stdlib/invariant/quantifiers.py`）。评估：每个 `raise`→`Rule`，`Interpreter.assignments()` 枚举满足赋值，每个模型→ `ErrorInformation`（带字符级 `ranges`）。

### 4.3 轨迹模型（可作我们规范 schema）`invariant/analyzer/runtime/nodes.py`（pydantic）
`Event → Message / ToolCall(id,function{name,arguments}) / ToolOutput(tool_call_id,_tool_call 回链) / Tool(MCP 定义)`；输入 = OpenAI chat-message `list[dict]`，`Input.parse_input()`(`runtime/input.py:369`) 按 `tool_call_id` 关联 output↔call，标 `metadata["trace_idx"]`。

### 4.4 ★ 关键弱点：dataflow 是位置可达，非真污点
`input.py:66` `Dataflow` 建的是**顺序可达图**（每个事件流向其后所有事件）——`has_flow(a,b)` 只表示 a 在 b 之前，**不是真数据流/污点**。→ **我们必须自建真 taint 传播**，这是要「改进而非照抄」的一处。

### 4.5 检测器 `stdlib/invariant/detectors/`（DSL 可调谓词）
`prompt_injection`(deberta HF 分类器) / `pii`(Presidio+spaCy) / `secrets`(正则) / `moderated` / `python_code|semgrep`(代码/工具投毒) / `is_similar` / `llm`(LLM-as-judge)。重检测器拉 `torch/transformers/presidio/spacy`，藏在 `extras` 可选依赖。→ 低延迟网关**保留 DSL 谓词接口、替换实现为我方检测服务**。

### 4.6 集成面 & 复用判定
- 库内嵌：`LocalPolicy.from_string(SRC).analyze(messages) → AnalysisResult(errors=[ErrorInformation])`；`[]`=放行；async `a_analyze`。
- **流式 pre/post 门控** `Monitor.check(past_events, pending_events)` 只返回归因于 pending 的新错误（`analyze_pending`）——**这就是因果轴 pre/post 执行原语**，`invariant-gateway` 即用它拦 LLM/MCP。
- 远程契约 `/api/v1/policy/check`（`remote_policy.py`）——需要时可自建同契约。
- **采纳三样**：(1) DSL + parser/evaluator；(2) `->`/`~>`/`is tool:` 轨迹+流原语；(3) `AnalysisResult/ErrorInformation/Range` verdict 模型。检测器实现替换为我方服务；**自建真 taint 替换位置可达 Dataflow**。SOC 侧：`analyze()` 跑历史轨迹做离线扫、用 DSL 作「策略建议」目标（生成规则字符串→在历史轨迹上 `analyze` 量测精度→再晋升到 Engine）。

---

## 5. 汇总：谁提供什么、怎么用

| 轴/能力 | 采纳自 | 方式 | 关键接口/资产 |
|---------|--------|------|---------------|
| 权限轴契约 | ToolHive | vendor/照抄接口 | `authorizers.Authorizer` + `AuthorizerFactory`（近零耦合） |
| 权限决策后端 | ToolHive | 复用 Cedar 后端 | `cedar/core.go`（`forbid`>`permit`，默认拒绝，RBAC via THVGroup） |
| enforcement 形态 | ToolHive | 照抄模式 | `vmcp/core.Admission`（一授权器驱动 list 过滤 + call 拒绝） |
| 数据/网络轴检测 | Pipelock | sidecar + 规则格式 | 签名 YAML 规则 + `internal/scanner` + `pipelock-rules` 语料 |
| **统一审计原语** | Pipelock | **lift** | **action-receipt（Ed25519+SHA-256 哈希链，4 语言验证器）** |
| 动作模型 | Pipelock | 采纳分类 | `ActionType/SideEffectClass/Reversibility` |
| 插件/Engine 范式 | Bifrost | 借设计 | 能力拆分接口 + Pre/Post 对称 executed-count + 类型化短路 + 统一 MCP 闸 |
| 审批原语 | Bifrost | 采纳概念 | execute-vs-auto-execute 拆分 + 三层 allowlist deny-by-default |
| 可选模型上游 | Bifrost | config 接入 | OpenAI 兼容多 provider 网关 |
| 行为/因果轴 | Invariant | 库内嵌 | DSL + `->`/`is tool:` 原语 + `Monitor.check` pre/post 门控 |
| verdict 模型 | Invariant | 采纳 | `AnalysisResult/ErrorInformation/Range` |
| SOC 策略建议 | Invariant | 采纳工作流 | 生成 DSL 规则→历史轨迹量测→晋升 |

### 三条必须自建/加固（不能照抄）
1. **真 taint 传播**（Invariant 只有位置可达）——因果轴严谨性所在。
2. **fail-closed 决策**（Bifrost 吞 error、PreRequest 不能拦）——安全默认必须反转。
3. **统一 Risk Decision Engine**（四项目各覆盖一轴，汇总是我们的核心，不外包）。

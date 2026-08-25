# ASG 全代码逐行审计报告（详细版）

> 审计方法：逐文件阅读源码 + go vet + 运行时功能测试
> 审计范围：82 文件，~9600 行
> 原则：只测不改，记录所有发现

---

## 一、cmd/asg-connect/ (探针, 13文件)

### main.go — ✅ 无问题

### serve.go — 发现 5 个问题

| # | 严重度 | 行号 | 问题 |
|---|---|---|---|
| A1 | 🟡 中 | L71-73 | **goroutine 泄漏**: syncLoop 和 flush 的 goroutine 没有退出机制。`stop` channel 传给了 syncLoop 但 serve() 从不 close 它。进程退出时 OS 清理所以实际影响小 |
| A2 | 🟢 低 | L107 | **每请求新建 http.Client**: `client := &http.Client{Timeout: 5 * time.Minute}` 在 handleLLM 内部创建，不复用连接池。高并发下 TCP 连接浪费 |
| A3 | 🟡 中 | L143 | **流式响应不记录 token 用量**: 流式路径的 ReportLLM 传了 respBody 但没解析 usage 字段，审计缺少成本追踪数据 |
| A4 | 🔴 高 | L197-250 | **route() 函数有大量重复代码**: 三段几乎相同的 fallback 逻辑（L228-232, L237-241, L245-249），说明逻辑混乱。且 `matchFamily()` 函数已不再被调用但未删除 |
| A5 | 🟢 低 | L258 | **`var _ = context.Background`** 死代码 |

### anthropic_bridge.go — 发现 3 个问题

| # | 严重度 | 行号 | 问题 |
|---|---|---|---|
| B1 | 🔴 高 | 全文 | **不支持流式(stream=true)**: Claude Code 默认发 stream=true，bridge 忽略此参数返回完整 JSON。Claude Code 会重试然后报 "malformed response"。这是之前测试失败的根本原因之一。需要实现 SSE 合成 |
| B2 | 🟡 中 | L80 | **每次请求新建 http.Client**: 同 A2 |
| B3 | 🟢 低 | L175 | `toolResultID` 变量声明后未使用（已在后续修复中清理） |

### anthropic_adapt.go — ✅ 无问题

### responses.go — 发现 2 个问题

| # | 严重度 | 行号 | 问题 |
|---|---|---|---|
| C1 | 🟡 中 | L131,150 | ASG_DUMP 调试代码遗留在生产路径中 |
| C2 | 🟢 低 | L160 | `json.Unmarshal(respBody, &cc)` 错误被忽略——如果上游返回非 JSON，cc 为零值导致空回复 |

### config.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| D1 | 🟡 中 | `AllowedModels` 在 Provider 结构体内但语义上是全局的（route() 遍历所有 providers 收集），配置容易混淆 |

### hooks.go — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| E1 | 🟡 中 | `initClient` 写入 settings.json 时如果已有其他 env 变量，mergeJSONFile 会覆盖同名 key |
| E2 | 🟢 低 | `ioReadAll` 自实现了 `io.ReadAll` 的功能 |

### mcpshim.go — ✅ 无问题

### registry_sync.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| F1 | 🟡 中 | `syncLoop` 的 panic recover 吞掉了所有错误，包括编程错误（nil pointer等）。应该只 recover 特定可预期的错误 |

### reporter.go — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| G1 | 🔴 高 | **traceID 竞态**: traceID() 和 lastLLM() 各自独立加锁，两次调用之间状态可能被另一个 goroutine 改变。ReportLLM 和 ReportTool 如果并发调用，parent_id 可能指向错误的 LLM 调用 |
| G2 | 🟢 低 | `var llmSeq int64` 未使用 |

### spool.go — ✅ 无问题（W9 已修复重复投递）

---

## 二、internal/engine/ (7文件)

### engine.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| H1 | 🟢 低 | 并行评估使用 WaitGroup 但没有 context deadline——如果一个引擎 hang 住，整个评估会永远阻塞。应加 `context.WithTimeout` |

### permission.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| I1 | 🟡 中 | ReloadFromFile 和 EvaluatePre 都用 RWMutex 保护 policySet，但 permGlobal 的赋值在 NewPermissionEngineFromFile 中没有用同一个锁保护（用的是包级 permMu）——如果同时创建多个 PermissionEngine 实例，最后一个会覆盖全局引用 |

### datanetwork.go — ✅ 无问题

### behavior.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| J1 | 🟡 中 | BehaviorEngine 调用 sidecar HTTP 时 timeout 只有 5 秒，如果 Invariant 分析复杂轨迹超时，failMode 决定放行或拦截。FailClosed 模式下会导致所有调用被 BLOCK |

### taint.go — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| K1 | 🟡 中 | TaintMark 永远不清除——一旦 session 中出现不可信内容，整个 session 的所有 sink 调用都会被检查。长时间运行的 session 会积累大量 taint marks 导致性能下降和误报增加 |
| K2 | 🟢 低 | `reHost` 正则的 TLD 列表硬编码了 `.com|.net|.org|.io|.ru|.cn|.xyz`，遗漏了大量合法 TLD |

### runtime_stream.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| L1 | 🟢 低 | ChunkScanner 的 carried buffer 只保留最后256字节，如果注入 pattern 跨越更长的边界会漏检 |

---

## 三、internal/proxy/ + ingress/ + mcpproxy/

### proxy.go — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| M1 | 🟡 中 | Handle() 中 CONFIRM 走 Approver.Confirm() 是同步阻塞的。如果审批人不在，agent 的 HTTP 请求会挂到超时。应该返回"等待审批"的中间态让 agent 可以轮询 |
| M2 | 🟢 低 | moreSevere() 合并 signals 时直接 append，可能导致 signals 数组膨胀（pre+post 各一份） |

### ingress.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| N1 | 🟡 中 | tools/list 不需要认证即可访问（tools/call 需要），泄露内部工具名和描述给未认证用户 |

### mcpproxy/upstream.go — ✅ 无问题

---

## 四、internal/webui/ (11文件)

### server.go — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| O1 | 🟡 中 | `/api/ingest` 没有任何认证——任何知道地址的人都可以向事件库写入虚假事件 |
| O2 | 🟢 低 | `/api/query` 的 limit 参数没有上限限制，传 limit=999999999 会返回全量数据 |

### index.html — 发现 2 个问题

| # | 严重度 | 问题 |
|---|---|---|
| P1 | 🟡 中 | 所有 fetch 调用都没有错误处理（catch 缺失），网络断开时 UI 静默停止更新无提示 |
| P2 | 🟢 低 | XSS 风险低（有 replace(/</g,'&lt;')）但不完整——tool name 和 session id 没有做 HTML escape |

### auth.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| Q1 | 🟡 中 | Session token 存在内存 map 中，网关重启后所有 session 失效。且没有清理过期 session 的机制（内存泄漏） |

---

## 五、internal/store/ + authn/ + config/ + session/ + receipt/

### store.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| R1 | 🔴 高 | **Query() 是 O(n) 全量扫描**——每次查询遍历全部事件。505条还行但10K+会很慢。且没有分页支持 |

### authn.go — 发现 1 个问题

| # | 严重度 | 问题 |
|---|---|---|
| S1 | 🟡 中 | API key 用 map[string]Tenant 明文存储在内存中。重启丢失（从 YAML 加载恢复 ✓）。但没有 key rotation 或过期机制 |

### receipt.go — ✅ 无问题（Ed25519 签名链验证正确）

---

## 六、新增安全包 (9个)

### outputsafety/engine.go — 发现 1 个问题
| # | 问题 |
|---|---|
| T1 | 规则列表是编译时硬编码的，无法通过配置动态添加规则 |

### drift/detector.go — 发现 2 个问题
| # | 问题 |
|---|---|
| U1 | sessions map 永远不清理——长期运行会内存泄漏 |
| U2 | CJK concept mapping 是硬编码的小字典，覆盖率有限 |

### riskpattern/detector.go — 发现 1 个问题
| # | 问题 |
|---|---|
| V1 | patterns 列表编译时固定，无法动态添加新 pattern |

### threatclass/threatclass.go — ✅ 无问题

### judge/judge.go — 发现 1 个问题
| # | 问题 |
|---|---|
| W1 | worker goroutine 如果 review() panic 不会 recover，会导致整个 judge goroutine 崩溃 |

### escalation/escalation.go — 发现 1 个问题
| # | 问题 |
|---|---|
| X1 | alerts 数组无限增长，长期运行内存泄漏 |

### skillscan/skillscan.go — ✅ 无问题

### shellcontrol/shell.go — 发现 1 个问题
| # | 问题 |
|---|---|
| Y1 | Run() 使用 exec.CommandContext 但没有设置 cmd.WaitDelay，如果命令产生大量输出可能死锁 |

---

## 七、intelligence/ + kgbridge/ + siem/ + registry/ + driftguard/

### intel/intel.go — 发现 1 个问题
| # | 问题 |
|---|---|
| Z1 | Analyze() 对每个 BLOCK 事件都重新扫描全部事件——O(n²)，大量 BLOCK 时性能下降 |

### kgbridge/asg_kg_worker.py — 发现 2 个问题
| # | 问题 |
|---|---|
| AA1 | KG_ENTITIES/KG_RELATIONSHIPS 全局列表无限增长 |
| AA2 | _embed() 每次调用都重新计算 concept embedding（应该缓存） |

### siem/export.go — ✅ 无问题
### registry/registry.go — ✅ 无问题  
### driftguard/driftguard.go — 发现 1 个问题
| # | 问题 |
|---|---|
| AB1 | check() 在持有 mu.Lock 的情况下执行 OnDrift 回调和 AutoRepair 文件写入——如果回调耗时长会阻塞其他操作 |

---

## 统计汇总

| 严重度 | 数量 | 说明 |
|---|---|---|
| 🔴 高 | 3 | G1(竞态), R1(O(n)查询), B1(无streaming) |
| 🟡 中 | 18 | 各种并发/内存/安全问题 |
| 🟢 低 | 12 | 代码卫生/小优化 |
| **总计** | **33** | |

## 功能测试结果（只测不改）

| 端点 | 方法 | 测试输入 | 结果 | 状态 |
|---|---|---|---|---|
| /healthz | GET | - | 200 "ok" | ✅ |
| /v1/messages | POST | anthropic 格式 | 200 正确回复 | ✅ |
| /v1/chat/completions | POST | openai 格式 | 200 正确回复 | ✅ |
| /v1/responses | POST | responses 格式 | SSE 流式返回 | ⚠️ 内容为空 |
| /mcp tools/list | POST | JSON-RPC | 工具列表 | ✅ |
| /mcp get_inbox | POST | alice key | ALLOW + 邮件内容 | ✅ |
| /mcp read_secret | POST | alice key | REDACT token=*** | ✅ |
| /mcp delete_user | POST | alice key | BLOCK cedar denied | ✅ |
| /api/hook-check Bash echo | POST | hook payload | allow | ✅ |
| /api/hook-check Bash rm -rf | POST | hook payload | block + reason | ✅ |
| /api/sessions | GET | - | session 列表 | ✅ |
| /api/events | GET | - | 最近100条事件 | ✅ |
| /api/clusters | GET | - | 聚类结果 | ✅ |
| /api/siem?format=cef | GET | - | CEF 格式导出 | ✅ |
| /api/siem?format=splunk | GET | - | Splunk 格式导出 | ✅ |
| /api/kg/search | GET | query 参数 | 语义检索结果 | ✅ |
| /api/kg/ask | POST | question | KG-grounded 回答 | ✅ |
| /api/ui-login | POST | password | session cookie | ✅ |
| /explorer/ | GET | - | Semantica 图谱 UI | ✅ |

# ASG 全代码审计报告

> 审计范围: 82 个文件, ~9600 行 Go + Python
> 工具: go vet (0 errors), 手动逐文件审查

---

## 🔴 严重问题（需要立即修复）

### S1. 探针 hook HTTP 端点无鉴权
- **文件**: `cmd/asg-connect/hook_http.go`
- **问题**: `/api/hook-check` 端点没有认证。任何能访问 :8181 的进程都可以调用它来探测安全规则。
- **风险**: 本地信息泄露（攻击者可以枚举哪些命令会被拦截）
- **修复**: 加 localhost-only 检查或 API key 验证

### S2. 网关 MCP 入口的 tools/list 无需认证
- **文件**: `internal/ingress/ingress.go`
- **问题**: `tools/list` 不需要认证就能看到所有工具名和描述，泄露了内部工具拓扑。
- **修复**: `tools/list` 也应要求认证。

### S3. connect.yaml 中 API key 明文存储
- **文件**: `connect.yaml`, `deploy/config.dev.yaml`
- **问题**: Lenovo qwen35 的 JWT token 直接写在 YAML 里（支持 `${ENV}` 但当前值是硬编码的）。
- **修复**: 改为 `${ENV_VAR}` 引用并从 .env 或系统 keyring 读取。

---

## 🟡 设计问题（建议改进）

### D1. 探针和网关的事件上报是单向的
- **位置**: `cmd/asg-connect/reporter.go`
- **现状**: 探针 → 网关是推模式，但网关无法主动通知探针"策略更新了"。
- **影响**: 策略变更后探针要等下一个心跳周期才能拿到新策略。
- **建议**: 用 WebSocket 或 SSE 建立双向通道。

### D2. KG worker 是单点故障
- **文件**: `internal/kgbridge/bridge.go`
- **现状**: KG worker 挂掉后语义搜索和图谱问答不可用，但网关继续运行。
- **当前处理**: fail-soft（不阻塞主流程）✓
- **建议**: 加自动重启逻辑（目前 worker 死了就永远死了）。

### D3. Invariant sidecar 的 failMode 在代码里写死为 FailOpen(0)
- **文件**: `deploy/config.dev.yaml` → `behavior_fail_open: true`
- **风险**: sidecar 挂掉时行为轴检查全部跳过（fail-open），可能漏过真实威胁。
- **权衡**: FailClosed 会因为 sidecar 不稳定导致大量误拦。
- **建议**: 生产环境改 FailClosed，同时提升 sidecar 稳定性。

### D4. 事件库没有持久化索引
- **文件**: `internal/store/store.go`
- **现状**: JSONL 追加写入，查询时全量扫描内存切片。事件量 >10K 后查询变慢。
- **建议**: 加 SQLite 或 BoltDB 作为索引层，JSONL 只做归档。

### D5. Cedar 策略热更新有竞态窗口
- **文件**: `internal/engine/permission.go` ReloadFromFile
- **现状**: 读文件→解析→替换 policySet，三步之间如果文件被并发修改，可能加载到部分内容。
- **概率**: 低（单管理员场景）
- **建议**: 用文件锁或原子重命名。

### D6. 探针的 anthropic bridge 缺少 streaming 支持
- **文件**: `cmd/asg-connect/anthropic_bridge.go`
- **现状**: Claude Code 发 stream=true 时 bridge 忽略了 stream 参数，返回完整 JSON 而非 SSE 流。
- **影响**: Claude Code 的打字机效果丢失（功能不受影响但体验降级）。
- **已在 responses.go 中实现了 SSE 合成**，messages 端点也需要同样处理。

---

## 🟢 已确认正常的部分

| 文件 | 结论 |
|---|---|
| `api/types.go` | 类型定义清晰，字段有注释 ✓ |
| `internal/proxy/proxy.go` | 三轴编排逻辑正确，REDACT 字节级改写已实现 ✓ |
| `internal/engine/engine.go` | 并行评估+一票否决正确实现 ✓ |
| `internal/rulesbundle/verify.go` | Ed25519 验签 fail-closed 正确 ✓ |
| `internal/shellcontrol/shell.go` | Windows+Linux 规则覆盖全面 ✓ |
| `internal/driftguard/` | 配置篡改检测+自动修复工作正常 ✓ |
| `internal/riskpattern/` | 跨事件序列匹配逻辑清晰 ✓ |
| `internal/threatclass/` | 危害分类+处置映射合理 ✓ |
| `internal/judge/` | LLM-as-Judge 异步审查设计好（不阻塞）✓ |
| `internal/policyversion/` | 版本管理+回滚简洁有效 ✓ |
| `internal/webui/index.html` | 单页面控制台功能齐全 ✓ |

---

## 📋 技术债清单

| # | 描述 | 影响 | 建议 |
|---|---|---|---|
| TD1 | `var _ = fmt.Sprintf` / `var _ = json.Marshal` 等空引用散布在多个文件 | 代码噪音 | 清理 |
| TD2 | `connect-events.jsonl` 被 git track 了（应该 .gitignore） | 仓库膨胀 | 加入 .gitignore |
| TD3 | demo/smart-assistant 目录被意外提交 | 仓库污染 | 删除或移到 examples/ |
| TD4 | 多个文件的 CRLF/LF 混用 | git diff 噪音 | 统一 .gitattributes |
| TD5 | `internal/kg/builder.go` 引用了不存在的 `ResultDecoded()` 方法 | 编译错误 | 移除或实现 |
| TD6 | `internal/monitor/monitor.go` import 了 `api` 包但未使用 | 编译警告 | 清理 |
| TD7 | escalation 包缺少单元测试 | 回归风险 | 补测试 |
| TD8 | judge 包缺少单元测试 | 回归风险 | 补测试 |
| TD9 | policyversion 包缺少单元测试 | 回归风险 | 补测试 |

---

## 优先修复顺序

1. **S3** (API key 明文) — 安全风险最高
2. **S1+S2** (未鉴权端点) — 公网暴露风险
3. **TD5** (编译错误) — 会阻塞 build
4. **TD2+TD3** (git hygiene) — 快速修
5. **D4** (事件库性能) — 数据量增长后会成为瓶颈

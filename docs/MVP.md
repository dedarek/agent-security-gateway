# MVP —— 最小可跑闭环 Demo 规划

目标：用最小代价跑通一条能对外演示的**安全闭环**：
`拦截 → 事件沉淀 → 人工确认 → 策略回写 → 下次提前拦截`。

对应 [PLAN.md](PLAN.md) 的 Phase 0 + Phase 1。

---

## 1. MVP 范围（只做一条轴，先打通闭环）

- 只做 **权限轴 (Axis A)**，数据/网络轴、行为轴留给后续 Phase。
- 接入面：MCP Proxy（Gateway 站在 Agent 与 filesystem/database MCP 之间）。
- 决策：`ALLOW / BLOCK / CONFIRM`（先不做 REDACT）。
- 闭环：事件写库 → Web 看轨迹 → 从 BLOCK 生成策略 → 热加载。

---

## 2. MVP 架构（尽量简化）

```
Agent ──MCP──▶ Gateway(Go) ──MCP──▶ 真实 MCP Server(filesystem/db)
                  │
                  │ Cedar 权限判断 (Axis A Engine)
                  │ ALLOW → 转发
                  │ BLOCK → 拒绝
                  │ CONFIRM → 挂起 → 飞书/CLI 审批
                  │
                  └── Event ──▶ SQLite/Postgres ──▶ 简易 Web (轨迹 + 审批 + 策略)
```

组件最小化：
- Gateway：Go，单进程。
- 策略：Cedar 策略文件（本地 `.cedar`），支持文件变更热加载。
- 存储：先用 SQLite。
- 审批：先用 CLI / 简单 Web 按钮（飞书集成放后面）。
- 前端：先一个极简 HTML/React 页看事件流。

---

## 3. Demo 剧本（可直接对外演示）

### 场景 1：直接拦截
```
员工 agent → database.delete_user(id=123)
Gateway: Cedar 命中 forbid(employee, delete_user) → BLOCK
前端: 显示 "已拒绝：员工角色禁止删除用户"，附证据
```

### 场景 2：敏感操作人工确认 (HITL)
```
员工 agent → database.export_all_users()
Gateway: 命中敏感操作规则 → CONFIRM → 挂起
管理员在审批页点 [批准]
Gateway: 放行本次调用，记录审批人 + 理由
```

### 场景 3：闭环学习
```
管理员在轨迹页看到 场景2 的 export_all_users
点 [固化为策略] → 生成 forbid(...export_all_users...) 草案 → 确认
Gateway 热加载新策略
再次调用 export_all_users → 这次直接 BLOCK（不再需要人工）
```

三个场景连起来，就完整展示了「发现 → 处置 → 学习 → 提前拦截」的闭环。

---

## 4. 实现步骤（对应代码骨架）

1. `cmd/gateway/main.go`：启动、加载 config、注册 Engine、起 MCP proxy。
2. `internal/proxy`：MCP 请求拦截 → 构造 `ToolCall` → 调 Engine → 按 verdict 转发/拒绝/挂起。
3. `internal/engine`：`Engine` 接口 + `PermissionEngine`（调 Cedar）。
4. `internal/policy`：加载 `.cedar` 策略 + 文件 watch 热加载。
5. `internal/audit`：`Event` 写 SQLite。
6. `internal/config`：yaml 配置（上游 MCP 地址、策略路径、DB）。
7. 极简 Web：事件列表 + 审批按钮 + 「固化为策略」按钮。

> 当前仓库已提供 Go 骨架（passthrough + Engine 接口 + stub 权限引擎），
> 见 `cmd/` 与 `internal/`。MVP 即在此骨架上填实 Cedar 与审批闭环。

---

## 5. 验收标准

- [ ] Agent 经 Gateway 正常调用 filesystem MCP（透传成功）
- [ ] 场景 1：delete_user 被 BLOCK，前端可见原因
- [ ] 场景 2：export_all_users 触发 CONFIRM，人工批准后放行
- [ ] 场景 3：固化策略后，export_all_users 下次直接 BLOCK
- [ ] 全流程事件可在 Web 轨迹页回溯

---

## 6. 明确不做（划清 MVP 边界）

- 不做数据/网络轴（SSRF/DLP）—— Phase 2
- 不做行为/因果轴（taint/行为链）—— Phase 3
- 不做 LLM 根因分析 —— Phase 4
- 不做高可用/多租户 —— Phase 5
- 审批先 CLI/Web，不先接飞书（虽然飞书 skill 可用，但 MVP 先最短路径）

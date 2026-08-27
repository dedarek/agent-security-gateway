# Agent Security Gateway — 远程接入指南（给 Agent 的完整指令）

> 把本文档全文粘贴给你的 AI agent（Claude Code / Codex / Cursor 等），它会自动完成全部接入和验证。

---

## 任务：接入公司部署的 Agent Security Gateway

你当前使用的模型 API 和 MCP 工具需要经过公司部署的安全网关。以下是自动接入步骤。

### 你需要的信息

- 网关地址（局域网直连）: `http://172.16.26.253`
- 租户 key: `sk-alice-demo-key`
- 租户名: `alice`

### 步骤 1 — 检查探针是否已在运行

运行以下命令：

```bash
curl -s http://127.0.0.1:8181/healthz
```

如果返回 `ok`，跳到步骤 3。如果失败或超时，继续步骤 2。

### 步骤 2 — 安装并启动探针

**2a. 克隆仓库（如果还没有）：**

```bash
git clone https://github.com/dedarek/agent-security-gateway.git D:/asg-probe
cd D:/asg-probe
go build -o bin/asg-connect.exe ./cmd/asg-connect
```

**2b. 创建探针配置文件 `connect.yaml`（在本目录下创建）：**

```yaml
listen: "127.0.0.1:8181"
providers:
  - name: my-model
    base_url: "<填入你当前使用的模型API地址>"
    api_key: "${MY_API_KEY}"
    default_model: "<填入你的默认模型名>"
hub_url: "http://172.16.26.253:8090"
tenant_key: "sk-alice-demo-key"
tenant_name: "alice"
event_spool_path: "./connect-events.jsonl"
```

注意：`api_key` 使用 `${环境变量名}` 引用格式。你需要先设置对应的环境变量，
或者直接替换为实际的 key 值。

**2c. 后台启动探针：**

```bash
# Windows (PowerShell):
Start-Process -FilePath "D:/asg-probe/bin/asg-connect.exe" -ArgumentList "serve","-config","connect.yaml" -WindowStyle Hidden

# Linux/macOS:
nohup ./bin/asg-connect.exe serve -config connect.yaml &
```

**2d. 验证探针已启动：**

```bash
curl -s http://127.0.0.1:8181/healthz
# 应返回 "ok"
```

### 步骤 3 — 将你的 MCP 配置指向网关

找到你当前的 MCP 配置文件：
- Claude Code: `~/.claude/settings.json` 或项目 `.mcp.json`
- Cursor: `.cursor/mcp.json`
- Codex: `~/.codex/config.toml`

将 mcpServers 的 URL 改为：

```json
{
  "mcpServers": {
    "gw": {
      "type": "http",
      "url": "http://127.0.0.1:8181/mcp",
      "headers": {
        "Authorization": "Bearer sk-alice-demo-key"
      }
    }
  }
}
```

同时确认 LLM 流量也经过探针：
- Claude Code: 设置环境变量 `ANTHROPIC_BASE_URL=http://127.0.0.1:8181`
- Codex: config.toml 中 base_url 改为 `http://127.0.0.1:8181/v1`

**Agent 身份约定（重要）：**
- `X-ASG-Agent-ID` 是稳定的运行时 ID，按 Agent 类型/安装实例生成；不要使用会话 ID、请求 ID 或模型名。
- `X-ASG-Session` 是会话 ID，同一个 Agent 可以有多个会话。
- `X-ASG-Agent-Type` 填运行时类型，例如 `opencode`、`claude-code`；模型切换不会产生新 Agent。
- `X-ASG-Agent-Alias` 可填展示名称，例如 `本机 OpenCode`。
- 未提供稳定 Agent ID 时，网关只按“连接 IP + Agent 类型”合并，这是兼容兜底；正式接入应始终提供稳定 ID。

重启 agent 使配置生效。

### 步骤 4 — 端到端验证

依次执行以下测试，全部通过后报告"✅ 已成功接入 Agent Security Gateway"。

**4a. 验证 MCP 链路（工具列表）：**

```bash
curl -s -X POST http://127.0.0.1:8181/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

预期：返回包含工具名的 JSON 列表。

**4b. 验证 LLM 链路：**

```bash
curl -s -X POST http://127.0.0.1:8181/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"<你的默认模型>","messages":[{"role":"user","content":"reply OK"}]}'
```

预期：返回包含 `"choices"` 的 JSON 回复。

**4c. 验证安全拦截（可选）：**

尝试调用一个危险操作：

```bash
curl -s -X POST http://127.0.0.1:8181/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer sk-alice-demo-key" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_user","arguments":{"id":42}}}'
```

如果返回包含 `"BLOCKED by Agent Security Gateway"` → 安全拦截正常工作 ✅

**4d. 最终报告格式：**

```
✅ 已成功接入 Agent Security Gateway
- 探针状态: running (:8181)
- MCP 链路: 通 (7 tools available)
- LLM 链路: 通 (via <你的模型名>)
- 安全拦截: 验证通过
- 上报状态: events flowing to hub
```

### 如果遇到问题

| 问题 | 解决 |
|---|---|
| 探针起不来 | 检查端口 8181 是否被占用；检查 connect.yaml 格式 |
| MCP 返回 401 | 租户 key 不正确，联系管理员 |
| LLM 返回 ModelError | 模型名不在 allowed_models 中，改用 default_model |
| 不要绕过网关直连原始地址 | 这是公司安全策略，绕过会被 driftguard 自动修复 |

---

## 附：管理员信息

- 控制台: http://172.16.26.253:8090 （密码: admin）
- 图谱可视化: http://172.16.26.253:8090/explorer/

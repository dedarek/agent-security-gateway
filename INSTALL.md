# Agent Security Gateway — 安装与接入教程

> 无侵入接入任何 MCP 兼容 agent。不改 agent 源码，不改工具服务器，用户保留模型自由。

## 架构一览

```
员工机器                              中央服务器
┌──────────────────────────┐         ┌─────────────────────────────┐
│ WorkBuddy / Claude Code  │         │  gateway.exe (ASG 中央网关)   │
│   │ LLM流量(模型随便配)    │         │  · 三轴风险引擎(权限/数据/行为) │
│   ▼                      │  事件上报 │  · 租户key · 签名回执链       │
│ asg-connect 探针:8181 ────┼────────►│  · 根因分析 · 攻击聚类 · 审批  │
│   │ MCP调用(自动带租户key) │         │  · 操作控制台 :8090           │
│   ▼                      │         └─────────────────────────────┘
│ 真实工具(upstream-mcp等)  │                    ▲
└──────────────────────────┘      防火墙只放行网关IP──┘
```

**强制原理**：agent 配置里只有探针一个地址。原始模型/工具地址从配置中删除，
网络上再封掉直连路径 → agent 每一次思考、每一个动作物理经过网关。

## 前置条件

- Windows 10/11（Linux/macOS 同理，路径换一下）
- Go 1.25+（编译用）或直接下载 release 二进制
- 一个你自己的模型 API key（OpenAI/Anthropic/OpenCode Zen 均可）

## 第一步：部署中央网关（管理员机器，一台即可）

```bash
git clone https://github.com/dedarek/agent-security-gateway.git
cd agent-security-gateway
go build -o bin/gateway.exe ./cmd/gateway
go build -o bin/upstream-mcp.exe ./cmd/upstream-mcp

# 配置租户（每个员工一把key）
cat > deploy/tenants.yaml << 'EOF'
tenants:
  - name: xiaoming
    api_key: sk-发给小明的key
    role: employee
    user_id: xiaoming@corp.com
    enabled: true
EOF

# 启动（网关 :8080 = agent入口；控制台 :8090）
bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml
```

验证：浏览器开 `http://网关IP:8090` 能看到控制台。

### （可选）配置中央 MCP 白名单

员工能用的工具由这里决定，探针会自动挂到他们的 agent 里：

```bash
curl -X POST http://localhost:8090/api/registry -H "Content-Type: application/json" \
  -d '{"name":"corp-tools","command":["C:/path/to/tool-server.exe"],"tenants":["xiaoming"]}'
```

## 第二步：员工机器装探针（每人一次，2分钟）

```bash
git clone https://github.com/dedarek/agent-security-gateway.git
cd agent-security-gateway
go build -o bin/asg-connect.exe ./cmd/asg-connect

# 写探针配置：模型和key是员工自己的，想用什么配什么
cat > connect.yaml << 'EOF'
listen: "127.0.0.1:8181"
providers:
  - name: my-provider            # 随便起名
    base_url: "https://api.openai.com/v1"   # 或 anthropic/zen/ollama 等任何兼容端点
    api_key: "${MY_API_KEY}"      # 支持 ${ENV} 引用，key 不落盘
    default_model: "gpt-4o"       # agent 发来未知名时路由到这里
    allowed_models: ["gpt-4o"]    # 可选：额度锁，只放行列表内模型
hub_url: "http://网关IP:8090"      # 中央网关
tenant_key: "sk-发给你的key"
tenant_name: "xiaoming"
event_spool_path: "./connect-events.jsonl"
EOF

# 启动探针（建议注册成开机自启，见 scripts/install-windows.sh）
bin/asg-connect.exe serve -config connect.yaml
```

## 第三步：一键把 agent 接进来（无侵入）

```bash
bin/asg-connect.exe init -app claude-code   # Claude Code
bin/asg-connect.exe init -app codex         # Codex CLI
bin/asg-connect.exe init -app cursor        # Cursor
```

这个命令只做一件事：**改 agent 的配置文件里的地址**——LLM base_url 指向本机探针、
MCP servers 指向探针垫片。原文件其他内容不动。

改完后重启 agent 即可。agent 看到的工具列表、使用方式完全不变。

### 各 agent 支持的接入方式明细

| Agent | LLM 流量 | MCP 工具 | 接入方式 |
|---|---|---|---|
| Claude Code | `ANTHROPIC_BASE_URL` 环境变量 | settings/mcp.json 指向探针 | 全支持 |
| Codex CLI (≥0.149) | config.toml `model_providers` | responses 协议经探针桥接 | 全支持 |
| Cursor | OpenAI 兼容 base_url | mcp.json 指向探针 | 全支持 |
| WorkBuddy 等任意 MCP 客户端 | 视产品而定 | mcpServers URL 改为探针 `/mcp` | MCP 必接 |

## 第四步：验证

```bash
# 1. 探针活着
curl http://127.0.0.1:8181/healthz        # → ok

# 2. 通过探针问一次模型
curl http://127.0.0.1:8181/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'

# 3. 打开管理员控制台 http://网关IP:8090
#    你的每一次对话、每一次工具调用都会实时出现在里面
```

## 安全能力速查

| 场景 | 网关行为 |
|---|---|
| Agent 正常干活 | 放行，无感 |
| 返回内容带密钥/PII | 自动打码后返回（REDACT） |
| 收到邮件/网页里的注入指令后调敏感工具 | 数据流 taint 判定 → BLOCK |
| 高危操作（删库/导出全员数据） | 挂起，管理员控制台弹审批，超时=拒绝 |
| 危险 shell 命令 | 本地规则+三轴引擎拦截 |
| 员工改配置绕过网关 | driftguard 检测并自动修复 |
| 进程直连外网 | egress 代理域名白名单兜底 |
| 事后追责 | Ed25519 签名回执链 + trace_id 因果回放 + SIEM 导出 |

## 常见问题

**Q: 会拖慢 agent 吗？**
本地探针转发延迟 ~1ms；三轴判定毫秒级并行。流式响应逐块透传，打字机效果不受影响。

**Q: 我的 API key 谁能看到？**
key 只存在你自己机器的 connect.yaml 里（支持 `${ENV}` 引用），上报的事件里密钥已被脱敏。

**Q: 断网了怎么办？**
探针本地缓存策略继续执法，事件落盘 spool，恢复后自动补报——监管不中断也不丢数据。

**Q: 模型能用哪些？**
探针协议适配 OpenAI/Anthropic 双格式，你的 key 能用的模型都能配。
已实测 OpenCode Zen 全系（kimi-k3 / glm-5.2 / minimax-m3 / ox-alpha-free）双协议+function calling。
`allowed_models` 可选做额度锁：不在名单内的模型名自动重映射到 default_model，
agent 无感、不会报错，运营者的额度也不会被意外消耗。

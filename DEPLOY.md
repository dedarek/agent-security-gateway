# Agent Security Gateway — 完整部署与接入指南

> 本文档覆盖：从零部署 → 公网暴露 → 远程 agent 无侵入接入 → 安全测试 → 事后回溯。
> 目标读者：部署者（管理员）+ 使用者（agent 操作者）。

---

## 第一部分：管理员部署（中央服务器，一次）

### 前置条件

- Windows / Linux / macOS
- Go 1.25+、Python 3.10+（KG/轨迹分析用）
- 一个 cpolar 或 ngrok 账号（公网隧道）

### 1.1 编译

```bash
git clone https://github.com/dedarek/agent-security-gateway.git
cd agent-security-gateway
go build -o bin/gateway.exe ./cmd/gateway
go build -o bin/upstream-mcp.exe ./cmd/upstream-mcp
go build -o bin/asg-connect.exe ./cmd/asg-connect
```

### 1.2 配置租户（谁可以用你的网关）

```yaml
# deploy/tenants.yaml
tenants:
  - name: xiaoming          # 员工标识
    api_key: sk-自定义key   # 发给员工，探针用它认证
    role: employee          # employee | admin
    user_id: xiaoming@corp.com
    enabled: true
  - name: secops
    api_key: sk-admin-key
    role: admin
    user_id: soc@corp.com
    enabled: true
```

### 1.3 配置文件说明

| 文件 | 用途 | 必改项 |
|---|---|---|
| `deploy/config.dev.yaml` | 网关主配置 | `kg_semantica_path`（Semantica 路径）|
| `deploy/tenants.yaml` | 租户身份 | 所有 key |
| `deploy/policies/permission.cedar` | 权限策略 | 按需 |
| `intelligence/analyzer/policy.iv` | 行为轴规则 | 按需 |

### 1.4 启动

```bash
# 终端1: 网关 (MCP入口 :8080, 控制台 :8090)
bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml

# 终端2: Invariant sidecar (行为轴DSL, 可选)
cd intelligence/analyzer && python sidecar.py --policy policy.iv --port 8901

# 终端3: KG worker (Semantica 图谱, 可选)
python internal/kgbridge/asg_kg_worker.py --port 8902 --semantica-path D:/proj/semantica
```

### 1.5 设置控制台密码（公网必须）

```bash
export ASG_UI_PASSWORD="你的强密码"
# Windows PowerShell:
$env:ASG_UI_PASSWORD = "你的强密码"
```

不设则默认 `admin`（仅限开发环境）。

---

## 第二部分：公网暴露（cpolar 隧道）

### 2.1 安装 cpolar

下载 https://www.cpolar.com → 安装 → 登录绑定 authtoken。

### 2.2 配置隧道

编辑 `~/.cpolar/cpolar.yml`：

```yaml
authtoken: 你的authtoken
tunnels:
  asg-console:
    proto: http
    addr: "8090"
```

### 2.3 启动

```bash
cpolar start asg-console
```

输出中会显示公网 URL，例如：
`https://xxxxxxxx.r20.vip.cpolar.cn`

### 2.4 安全注意

- 探针的额度锁 (`allowed_models`) 在**员工本机配置**里设，网关不管这个
- 控制台密码**必须设置**（公网访问会强制要求登录）
- MCP 入口 (:8080) 已有租户 key 鉴权，公网可安全暴露
- Semantica Explorer 已反代进控制台 `/explorer/`，无需单独暴露

---

## 第三部分：远程 Agent 接入（员工电脑，每台 2 分钟）

### 3.1 你需要的两样东西（找管理员要）

| 信息 | 示例 |
|---|---|
| 网关公网地址 | `https://xxxx.r20.vip.cpolar.cn` |
| 你的租户 key | `sk-alice-demo-key` |

### 3.2 安装探针

```bash
git clone https://github.com/dedarek/agent-security-gateway.git
cd agent-security-gateway
go build -o bin/asg-connect.exe ./cmd/asg-connect
```

创建 `connect.yaml`：

```yaml
listen: "127.0.0.1:8181"
providers:
  - name: my-model                    # 你的模型提供方
    base_url: "https://api.openai.com/v1"  # 或 anthropic/zen/ollama
    api_key: "${MY_API_KEY}"          # 支持 ${ENV} 引用
    default_model: "gpt-4o"
    allowed_models: ["gpt-4o"]        # 可选：额度锁
hub_url: "https://xxxx.r20.vip.cpolar.cn"  # 管理员的公网地址
tenant_key: "sk-alice-demo-key"         # 管理员发给你的
tenant_name: "xiaoming"                 # 你的租户名
event_spool_path: "./connect-events.jsonl"
```

启动：

```bash
export MY_API_KEY=sk-你的真实key
bin/asg-connect.exe serve -config connect.yaml
```

### 3.3 一键接入 Agent

```bash
# Claude Code
bin/asg-connect.exe init -app claude-code

# Codex CLI
bin/asg-connect.exe init -app codex

# Cursor
bin/asg-connect.exe init -app cursor
```

这会自动修改 agent 的配置文件（LLM 地址→探针、MCP→探针垫片），不改任何源码。

### 3.4 手动接入其他 MCP 客户端

如果 agent 不在上述列表里，手动改它的 MCP 配置：

```json
{
  "mcpServers": {
    "gw": {
      "type": "http",
      "url": "http://127.0.0.1:8181/mcp",
      "headers": { "Authorization": "Bearer sk-alice-demo-key" }
    }
  }
}
```

LLM 流量同理：把 base_url 改为 `http://127.0.0.1:8181/v1`。

---

## 第四部分：给 Agent 的自我接入指令（复制粘贴即用）

> 把下面这段话直接粘贴给你的 AI agent（Claude Code / Codex / Cursor 等），它会自动完成全部接入和验证。

```
请帮我接入公司部署的 Agent Security Gateway。按照以下步骤操作，每步完成后验证再进行下一步。

## 背景
我们公司部署了一个 AI 安全网关，所有 agent 的 LLM 流量和工具调用都要经过它。你现在的任务是把自己接入这个网关。

## 你需要的信息（已由管理员提供）
- 网关地址: <在这里填入管理员的公网URL>
- 租户 key: <在这里填入你的key>
- 探针本地端口: 8181

## 步骤

### 步骤1: 检查是否已有探针在运行
运行: curl -s http://127.0.0.1:8181/healthz
如果返回 "ok"，跳到步骤3。如果失败，继续步骤2。

### 步骤2: 安装并启动探针
2a. 克隆仓库（如果还没有）:
    git clone https://github.com/dedarek/agent-security-gateway.git D:/asg-probe
2b. 编译:
    cd D:/asg-probe && go build -o bin/asg-connect.exe ./cmd/asg-connect
2c. 创建 connect.yaml（写入以下内容，替换尖括号里的值）:
    ---
    listen: "127.0.0.1:8181"
    providers:
      - name: my-provider
        base_url: "<你当前用的模型API地址>"
        api_key: "${<你当前模型的key环境变量名>}"
        default_model: "<你当前的默认模型>"
    hub_url: "<网关地址>/api"
    tenant_key: "<租户key>"
    tenant_name: "<你的名字>"
    event_spool_path: "./connect-events.jsonl"
    ---
    注意: api_key 用 ${环境变量名} 引用，不要写明文key。
2d. 后台启动探针:
    在后台运行: bin/asg-connect.exe serve -config connect.yaml
2e. 验证: curl -s http://127.0.0.1:8181/healthz 应返回 "ok"

### 步骤3: 把自己的 MCP 配置指向网关
3a. 找到你当前使用的 MCP 配置文件（可能在 .mcp.json / settings.json / config.toml）
3b. 把里面的 mcpServers 条目的 url 改为: http://127.0.0.1:8181/mcp
    并加上 headers: {"Authorization": "Bearer <租户key>"}
    （探针会自动注入正确的凭据转发到网关）
3c. 如果你是 Claude Code，还需要确认 ANTHROPIC_BASE_URL=http://127.0.0.1:8181

### 步骤4: 端到端验证
4a. 通过网关调用一个 MCP 工具:
    curl -s -X POST http://127.0.0.1:8181/mcp \
      -H "Content-Type: application/json" \
      -H "Accept: application/json, text/event-stream" \
      -H "Authorization: Bearer <租户key>" \
      -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
    如果返回了工具列表 JSON → MCP 链路通。
4b. 通过探针调一次 LLM:
    curl -s -X POST http://127.0.0.1:8181/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{"model":"<你的默认模型>","messages":[{"role":"user","content":"reply OK"}]}'
    如果返回了 AI 回复 → LLM 链路通。
4c. 两项都通过 → 报告"✅ 已成功接入 Agent Security Gateway"

### 步骤5: 如果遇到问题
- 探针没起来: 检查 connect.yaml 格式和端口 8181 是否被占用
- 工具调用 401: 检查租户 key 是否正确
- LLM 返回错误: 检查 providers.base_url 和 api_key 是否指向你有效的模型端点
- 不要绕过网关直连原始地址——这是公司安全策略
```

---

## 第五部分：安全测试手册（验证网关效果）

部署完成后，用以下测试集验证每个安全能力。通过 MCP 发请求即可（curl 或让 agent 做）。

### 测试矩阵

| # | 场景 | 操作 | 预期结果 | 验证轴 |
|---|---|---|---|---|
| T1 | 正常读数据 | `get_inbox` | ALLOW，无打扰 | — |
| T2 | 读密钥文件 | `read_secret` | REDACT，返回 `token=***` | 数据轴 DLP |
| T3 | 越权删用户 | employee 角色调 `delete_user` | BLOCK，Cedar 策略拒绝 | 权限轴 |
| T4 | admin 删用户 | admin 角色调 `delete_user` | ALLOW（admin 有权限） | 权限轴 |
| T5 | 邮件注入→外发 | 先 `get_inbox` 再 `send_email(to=attacker@gmail.com)` | BLOCK，taint 数据流拦截 | 行为轴 |
| T6 | 批量导出 | `export_all_users` | BLOCK（Invariant DSL）/ CONFIRM（审批） | 行为轴+权限轴 |
| T7 | 危险 shell | `rm -rf /` 经 shell gate | BLOCK，本地规则 | 数据轴 |

### 测试命令模板

```bash
curl -s -X POST http://127.0.0.1:8181/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer <你的租户key>" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"<工具名>","arguments":{...}}}'
```

### 审批流测试（CONFIRM 场景）

1. 发起敏感操作 → agent 调用会挂起
2. 打开控制台 → 审批队列出现请求（浏览器会通知）
3. 点「批准」→ agent 收到结果继续执行
4. 点「拒绝」→ agent 收到 `human denied confirmation`
5. 不处理 → 120秒后超时自动拒绝（fail-closed）

### 事后回溯

| 功能 | 怎么看 |
|---|---|
| **轨迹回放** | 控制台 → 点左侧 session → 右侧逐帧时间线 |
| **根因分析** | 同页面下方，自动标注因果链 |
| **一键策略下发** | 根因下方的 Cedar 建议 → 点接受 → 热生效 |
| **攻击聚类** | 左侧面板，跨 session 聚合相同模式 |
| **图谱可视化** | 点「🌐打开交互式图谱」→ 拖拽节点、沿边追溯 |
| **SIEM 导出** | `GET /api/siem?format=cef` 或 `?format=splunk` |
| **签名回执** | 每个事件 Ed25519 签名，`receipt.VerifyChain()` 可验 |

---

## 第六部分：故障排查

| 问题 | 原因 | 解决 |
|---|---|---|
| 探针起不来 | 端口被占 | 改 connect.yaml 的 listen 端口 |
| MCP 401 | 租户 key 错 | 检查 Authorization header |
| LLM 返回 ModelError | 模型名不在 allowed_models | 加到白名单或改 default_model |
| 控制台打不开(公网) | 未设密码 | 设 `ASG_UI_PASSWORD` 后重启网关 |
| 事件没上报 | hub_url 不对或网络不通 | 检查 spool 文件是否有积压；hub恢复后自动补发 |
| Invariant 全部 BLOCK | sidecar 的 failMode 是 FailClosed | config 设 `behavior_fail_open: true` |

---

## 附录：端口一览

| 端口 | 服务 | 暴露建议 |
|---|---|---|
| :8080 | 网关 MCP 入口 | 公网（有租户 key 保护） |
| :8090 | 控制台 + API + Explorer 代理 | 公网（有密码保护） |
| :8181 | 探针本地入口 | 仅 localhost（不暴露） |
| :8901 | Invariant sidecar | 仅 localhost |
| :8902 | KG worker | 仅 localhost |

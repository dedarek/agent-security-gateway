# Universal Gateway Onboarding（通用网关接入声明）

> **Harness-agnostic principle（接入与 harness 无关）**：任意 agent 只要能发 OpenAI 兼容 API（`/v1/chat/completions` 等）+ MCP（`/mcp` JSON-RPC），即可通过本声明一次接入，无需为 `claude-code / codex / cursor / opencode` 单独分支。模型、provider、工具链均由 Sidecar 嗅探，不再手填。

---

## 1. 通用声明文件 `universal.json`

**固定路径（harness 零新增）：**

```
~/.config/asg/universal.json        # Linux / macOS
%USERPROFILE%\.config\asg\universal.json   # Windows
```

> `~/.config/asg/` 由安装脚本自动创建；声明文件是**唯一**需要写入的配置文件。不会再写 `~/.claude/settings.json`、`~/.codex/config.toml`、`~/.cursor/mcp.json` 等 harness 专属文件。

**三字段（不含 `default_model`）：**

| 字段 | 类型 | 必填 | 说明 | 示例 |
|------|------|------|------|------|
| `hub_url` | string | ✅ | 中央网关地址（公网或局域网直连） | `https://asg-gateway.vip.cpolar.cn` 或 `http://172.16.26.253:8090` |
| `tenant_key` | string | ✅ | 租户密钥（控制台分配，Bearer 鉴权） | `sk-asg-***` |
| `listen` | string | ⭕ | Sidecar 本地监听地址，缺省 `127.0.0.1:8181` | `127.0.0.1:8181` |

**与旧 `connect.yaml` 的关系：**

- `universal.json` 不含 `providers / default_model / api_key` —— 模型由 Sidecar 从真实请求体 `{"model":"..."}` 嗅探（`observed_model`），provider 由路由推断，不再手填。
- 旧 `connect.yaml` 仍可读（`Providers` 兼容），但新安装只写 `universal.json`。
- `tenant_name / agent_id / agent_type / agent_alias` 等可选字段由 Sidecar 根据本机信息自动生成，无需声明。

---

## 2. 文件示例

**最小可用（仅三字段）：**

```json
{
  "hub_url": "https://asg-gateway.vip.cpolar.cn",
  "tenant_key": "***",
  "listen": "127.0.0.1:8181"
}
```

**公网接入完整示例：**

```json
{
  "hub_url": "https://asg-gateway.vip.cpolar.cn",
  "tenant_key": "sk-asg-alice-xxx",
  "listen": "127.0.0.1:8181"
}
```

**局域网直连示例：**

```json
{
  "hub_url": "http://172.16.26.253:8090",
  "tenant_key": "sk-asg-local-xxx",
  "listen": "127.0.0.1:8181"
}
```

> `listen` 可省略，默认为 `127.0.0.1:8181`。Sidecar 同时在此端口提供：
> - `POST /v1/*` — LLM 透明代理（OpenAI 兼容，任意 `model` 直通）
> - `POST /mcp` — MCP 聚合代理（`tools/list` 扇出，多上游工具聚合）
> - `GET /healthz` — 存活探针，返回 `ok`

---

## 3. Sidecar 行为（通用路径）

```
[任意 Agent] ──► 127.0.0.1:8181/v1/*  ──► 真实 Provider（透传 model）
             ──► 127.0.0.1:8181/mcp   ──► 上游 MCP Servers（扇出聚合）
             ───────────────────────────────────────► Hub (hub_url)
                        ▲  观测值驱动
                        │  observed_model / observed_ips 由流量提
                        │  无 providers 时仍可用（模型嗅探）
```

- **模型嗅探**：Sidecar 解析每次 `/v1/*` 请求体中的 `model` 字段，写入 `observed_model/provider`，网关与控制台以此为准，不再读 `Providers[0].DefaultModel`。
- **活躍判定**：网关 `active` 以最近事件驱动，心跳仅判 `idle/offline`。
- **Provider 兼容**：若本机仍有 `connect.yaml`（含 `providers`），Sidecar 优先用其路由；否则所有 `model` 直通首个可达 provider（嗅探值）。

---

## 4. 安装（通用一键）

> 前置：`sh` + `curl`，无 `sudo`、无二进制下载以外的常驻进程。

```bash
# 1. 写入通用声明（三字段）
mkdir -p ~/.config/asg
cat > ~/.config/asg/universal.json <<EOF
{
  "hub_url": "https://asg-gateway.vip.cpolar.cn",
  "tenant_key": "<TENANT_KEY>",
  "listen": "127.0.0.1:8181"
}
EOF
chmod 600 ~/.config/asg/universal.json

# 2. 启动 Sidecar（后台）
asg-connect serve -config ~/.config/asg/universal.json &

# 3. 验证
curl -s http://127.0.0.1:8181/healthz           # => ok
curl -s http://127.0.0.1:8181/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'

# 4. Agent 侧只需将 Base URL / MCP URL 指向 Sidecar（OpenAI 兼容通用）
#    LLM:  base_url = http://127.0.0.1:8181/v1
#    MCP:  url      = http://127.0.0.1:8181/mcp
```

> `asg-connect serve --dry-run -config ~/.config/asg/universal.json` 可打印通用声明解析结果，不启动端口（用于 CI 校验）。

---

## 5. 与 Harness 无关的验证

```bash
# 任意自定义 agent（agent_type=custom）发送一条 LLM 请求后：
curl -s http://127.0.0.1:8181/healthz | grep ok

# 网关侧应出现新 agent，且 model 自动识别为请求中的 model
curl -s $hub_url/api/agents | jq '.[] | {agent_id, model, provider, status}'

# 通用 MCP 聚合口（不写 ~/.claude / ~/.cursor）
cat ~/.config/asg/mcp.json
# => {"mcpServers":{"asg":{"url":"http://127.0.0.1:8181/mcp"}}}
```

---

## 6. 配置结构（`cmd/asg-connect/config.go`）

```go
type ProbeConfig struct {
    Listen        string     `yaml:"listen" json:"listen"`
    Providers     []Provider `yaml:"providers" json:"providers,omitempty"` // 兼容旧 connect.yaml
    HubURL        string     `yaml:"hub_url" json:"hub_url"`
    TenantKey     string     `yaml:"tenant_key" json:"tenant_key"`
    UniversalPath string     `yaml:"universal_path" json:"universal_path"` // ~/.config/asg/universal.json
    // ... tenant_name / agent_id / agent_type / agent_alias 等自动生成，不依赖 Providers[0].DefaultModel
}
```

- `UniversalPath`：通用声明路径，默认 `~/.config/asg/universal.json`；`asg-connect serve` 未显式传 `-config` 时自动探测。
- `Providers`：保留以兼容历史 `connect.yaml`；新 `universal.json` 可完全省略。
- `AgentAlias`：不再从 `Providers[0].DefaultModel` 推导，改为 `agent_type + hostname` 或显式 `agent_alias`。

---

## 7. 排障

| 现象 | 处置 |
|------|------|
| `curl 127.0.0.1:8181/healthz` 超时 | 检查 `universal.json` 的 `listen` 是否被占用；`ps aux | grep asg-connect` |
| `api/agents` 看不到新 agent | 检查 `hub_url / tenant_key` 是否正确；Sidecar 日志是否有 `agent registered` |
| 旧 `connect.yaml` 与 `universal.json` 并存 | 以显式 `-config` 为准；未显式时优先 `universal.json`，回退 `connect.yaml` |

---

## 8. 回滚

```bash
pkill asg-connect 2>/dev/null; rm -rf ~/.config/asg/universal.json
# 旧 harness 专属 hook 残留不影响通用路径；如需清理按对应 harness 的 settings.json 手动移除 asg 段即可
```

---

## 9. 控制台展示（`agent_type=custom` 通用接入）

> **前端构建校验（2026-08-30）**：`npm --prefix web run build` 成功（`vite v8.2.2, 101 modules, 324.77 kB`），`web/dist` 与 `internal/webui/dist` 哈希一致（`go:embed all:dist`），`wsl go vet ./...` 通过。通用 `custom` 类型在控制台按“通用”样式展示，模型/provider 按品牌徽标展示。

### 9.1 Live 实时台 — 通用 agent 卡片

- **类型芯片**：`agent_type=custom` 显示为 `通用 · custom`（dashed 中性样式，`title="通用接入（harness-agnostic, agent_type=custom）"`），与 `claude-code / opencode / cursor / codex` 的品牌 `BrandChip` 区分（后者带 SVG logo）。
- **模型芯片**：嗅探到的 `observed_model / model` 按 `web/src/assets/logos/` 15 品牌映射渲染（`openai / anthropic / google(gemini) / grok / zhipu(glm) / kimi(moonshot) / qwen / minimax / deepseek / tencent_hunyuan ...`），命中时为带 logo 的 `BrandChip`，未命中时为文本芯片；未上报时显示 `模型未上报`（dashed）。
- **Provider 芯片**：同模型映射，`provider !== model` 时追加虚线芯片。
- **来源徽章**：`observed`（绿色）表示网关由真实 `model` 字段嗅探，`self-reported` 为旧 `connect.yaml` 回退。

```
[通用 · custom] [BrandChip: gpt-4o/openai-logo] [openai dashed] observed
[通用 · custom] [BrandChip: qwen3/logo] observed
[claude-code BrandChip] [anthropic logo] ...
```

### 9.2 舰队页与详情

- 舰队表格“模型”列同样通过 `logoFor(model||provider)` 渲染 16px `BrandLogo`。
- 详情页标题区同 Live 卡片逻辑。

### 9.3 截图占位

> 截图取自 `http://127.0.0.1:8090/`（构建产物 `internal/webui/dist`，SPA shell 已开放匿名只读）。如需更新截图，按下述步骤重建后截屏替换 `docs/img/`。

- `docs/img/live-universal-custom.png` — Live 实时台：顶部 KPI + 威胁等级 + 1 张 `通用 · custom` 卡片（含 `BrandChip` 模型 logo + `observed` 徽章）与 1 张 `claude-code` 对比卡片。
- `docs/img/fleet-universal-custom.png` — 舰队页：表格中 `agent_type=custom` 行与品牌 logo 模型列并排。
- `docs/img/drawer-universal-control.png` — 抽屉：`能力管控 →` 嵌套二级抽屉（allow/confirm/block 分段控件）。

> 当前仓库未入库实机截图时，以上占位以文字描述为准；CI 环境执行 `npm --prefix web run build && cp -r web/dist/* internal/webui/dist/` 后以真实控制台截图替换即可。下次截图更新提交信息统一为 `docs: universal onboarding screenshots`。

### 9.4 本地复现（截图前）

```bash
# 1. 构建前端并同步 embed
npm --prefix web run build
rm -rf internal/webui/dist && mkdir -p internal/webui/dist && cp -r web/dist/* internal/webui/dist/

# 2. 启动网关（或仅校验构建）
go vet ./... && go build ./...

# 3. 注册通用 agent（任意 harness 以 custom 接入，发送一条带 model 的请求后）
curl -s http://127.0.0.1:8090/api/agents | jq '.[].agent_type'
# => "custom"

# 4. 打开控制台 http://127.0.0.1:8090/ 截图 Live / Fleet
```

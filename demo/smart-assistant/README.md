# 企业智能助手 — ASG 全链路可观测 Demo

> 一个“能干但危险”的企业 AI 助手，所有流量强制走 ASG 全链路，网关侧完整可观测。

```
浏览器/CLI
  ↓
智能助手 (LLM + MCP 工具)
  ↓  http://127.0.0.1:8181  (asg-connect 探针)
  ↓  http://127.0.0.1:8080/8090  (Agent Security Gateway 三轴引擎)
  ↓
上游 MCP (get_inbox / read_secret / send_email / ...)
```

## 网关可观测什么

- **轨迹回放**：每个 session 的逐帧 `ALLOW / BLOCK / REDACT / CONFIRM`
- **三轴判定**：权限轴(Cedar)、数据轴(Pipelock DLP)、行为轴(Taint)
- **根因+策略建议**：自动生成 Cedar 策略，一键热下发
- **探针事件**：LLM 输入输出、工具参数/结果，全量上报 `8090`

## 快速开始

### 方式 A — CLI 一键跑全链路验证

```bash
cd demo/smart-assistant
python agent_demo.py
# 或指定 session 名
python agent_demo.py --session demo-20260824
```

会依次触发：
1. LLM 调用（经探针）
2. `get_inbox` / `read_customer_db` → ALLOW
3. `read_secret` → REDACT（密钥脱敏）
4. `delete_user` → BLOCK（权限）
5. `export_all_users` → CONFIRM/BLOCK
6. `send_email(可信)` → ALLOW
7. Taint 攻击链 `get_inbox → send_email(attacker@gmail.com)` → BLOCK
8. 自动拉 `8090/api/trajectory` 验证网关已落盘

结束后打开控制台查看：
`http://127.0.0.1:8090` → 左侧 Sessions 找到你的 session → 右侧看轨迹

### 方式 B — Web 可交互系统

```bash
pip install flask
python app.py
# 打开 http://127.0.0.1:5001
```

点按钮触发各类操作，右侧实时拉网关轨迹。所有按钮操作都走全链路。

## 前置条件

- 网关：`./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml` （:8080 + :8090）
- 探针：`./bin/asg-connect.exe serve -config connect.yaml` （:8181）

`scripts/run_gw.sh` / `scripts/run_probe.sh` 已配置好。

## 验证全链路通

```bash
curl http://127.0.0.1:8181/healthz  # -> ok
curl http://127.0.0.1:8090/api/sessions | python -m json.tool
```

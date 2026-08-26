# ASG 接入凭据（内部文档，勿外传）

## 中央网关
- 局域网地址: http://172.16.26.253 （本机不跑 cpolar，同网段直连）
  - MCP 入口: http://172.16.26.253:8080/mcp
  - 控制台: http://172.16.26.253:8090
- 控制台密码: admin (环境变量 ASG_UI_PASSWORD 可改)

## 租户 Key

| 租户 | Key | 角色 |
|---|---|---|
| alice | sk-alice-demo-key | employee |
| secops | sk-secops-demo-key | admin |

## 探针配置示例(connect.yaml 中的关键行)
    hub_url: "http://172.16.26.253:8090"
    tenant_key: "sk-alice-demo-key"
    tenant_name: "alice"

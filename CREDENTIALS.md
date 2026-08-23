# ASG 接入凭据（内部文档，勿外传）

## 中央网关
- 公网地址: https://6445154b.r24.cpolar.top
- 控制台密码: admin (环境变量 ASG_UI_PASSWORD 可改)

## 租户 Key

| 租户 | Key | 角色 |
|---|---|---|
| alice | sk-alice-demo-key | employee |
| secops | sk-secops-demo-key | admin |

## 探针配置示例(connect.yaml 中的关键行)
    hub_url: "https://6445154b.r24.cpolar.top"
    tenant_key: "sk-alice-demo-key"
    tenant_name: "alice"

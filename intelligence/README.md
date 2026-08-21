# Intelligence / SOC (Analysis Plane)

分析面：消费 Gateway 沉淀的事件，做**轨迹还原 → 根因分析 → 攻击模式挖掘 → 策略建议**，
人工确认后把策略回写 Gateway，形成闭环。语言选 **Python**（行为链/因果/LLM 分析迭代快）。

对应 [../docs/PLAN.md](../docs/PLAN.md) 的 Phase 3（行为轴）与 Phase 4（分析闭环）。

## 模块（规划）

```
intelligence/
├── analyzer/
│   ├── trajectory.py     # 把 session 内事件聚合成行为轨迹 + taint 传播
│   ├── rootcause.py      # 因果还原：输入源 → 中间步骤 → 危险动作
│   ├── patterns.py       # 从重复攻击中挖掘模式
│   ├── suggest.py        # 生成 Cedar / Guardrails DSL 策略草案 + 影响面评估
│   └── consumer.py       # 从 NATS/Kafka 消费 Event
└── README.md
```

## 数据契约

消费的 `Event` schema 与 Gateway 完全一致（由 `api/proto` 生成，Go/Python 共用）。
当前 Go 侧定义见 [`../api/types.go`](../api/types.go)。

## 闭环

```
Event ──▶ trajectory ──▶ rootcause ──▶ suggest(策略草案)
                                          │
                            控制台 管理员 接受/修改/拒绝
                                          │
                                          ▼
                              Policy Store (Cedar/DSL) ──热加载──▶ Gateway
```

> 招牌场景：`read_email → 恶意邮件 → read_customer_db → send_email(external)`
> 三步单看都合法，行为轴串起来识别为间接 Prompt Injection，根因定位 + 生成
> 「外部邮件内容不得作为敏感数据库访问的授权依据」策略。见 ../README.md §3。

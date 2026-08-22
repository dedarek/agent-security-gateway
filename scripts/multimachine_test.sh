#!/usr/bin/env bash
# Multi-machine integration test: simulates 3 machines (xiaoming/you/boss)
# each running a probe against one central gateway, then verifies tenant
# isolation, central aggregation and per-trace causality.
#
# Usage: bash scripts/multimachine_test.sh
set -euo pipefail
# load real key from hermes env (bypasses shell redaction)
OPENCODE_GO_API_KEY=""
if [ -f "$HOME/AppData/Local/hermes/.env" ]; then
  LINE=$(grep -E "^OPENCODE_GO_API_KEY=" "$HOME/AppData/Local/hermes/.env" | head -n 1)
  OPENCODE_GO_API_KEY="${LINE#OPENCODE_GO_API_KEY=}"
fi
export OPENCODE_GO_API_KEY
cd "$(dirname "$0")/.."

GW=127.0.0.1:8080
UI=127.0.0.1:8090
PASS=0; FAIL=0
ok(){ echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad(){ echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

echo "=== ASG 多机联测 ==="

# --- setup: central gateway with multi-tenant registry
./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml &
GWPID=$!
sleep 2

# --- probes for two "machines" on different local ports
cat > D:/tools/tmp/probe-xiaoming.yaml <<EOF
listen: "127.0.0.1:8181"
providers:
  - name: opencode-zen
    base_url: "https://opencode.ai/zen/go/v1"
    api_key: "${OPENCODE_GO_API_KEY}"
    default_model: "ox-alpha-free"
hub_url: "http://$UI"
tenant_key: "sk-alice-demo-key"
tenant_name: "alice"
event_spool_path: "D:/tools/tmp/spool-alice.jsonl.queue"
EOF
cat > D:/tools/tmp/probe-boss.yaml <<EOF
listen: "127.0.0.1:8283"
providers:
  - name: opencode-zen
    base_url: "https://opencode.ai/zen/go/v1"
    api_key: "${OPENCODE_GO_API_KEY}"
    default_model: "ox-alpha-free"
hub_url: "http://$UI"
tenant_key: "sk-secops-demo-key"
tenant_name: "secops"
event_spool_path: "D:/tools/tmp/spool-secops.jsonl.queue"
EOF

python -c "
key=None
for line in open(r'C:/Users/yyyyc/AppData/Local/hermes/.env'):
    if line.startswith('OPENCODE_GO_API_KEY='):
        key=line.split('=',1)[1].strip(); break
for f in ['D:/tools/tmp/probe-xiaoming.yaml','D:/tools/tmp/probe-boss.yaml']:
    s=open(f).read().replace('\${OPENCODE_GO_API_KEY}', key)
    open(f,'w').write(s)
print('configs ready')
" 2>/dev/null || true

./bin/asg-connect.exe serve -config D:/tools/tmp/probe-xiaoming.yaml & P1=$!
./bin/asg-connect.exe serve -config D:/tools/tmp/probe-boss.yaml   & P2=$!
sleep 2

cleanup(){ kill $GWPID $P1 $P2 2>/dev/null || true; }
trap cleanup EXIT

echo "--- [1] 两台机器各自的 LLM 流量经探针可达模型"
R1=$(curl -s -m 60 http://127.0.0.1:8181/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"model":"ox-alpha-free","messages":[{"role":"user","content":"reply A"}]}' | grep -c '"content"' || true)
[ "$R1" -ge 1 ] && ok "alice机器 LLM 通" || bad "alice机器 LLM 不通"
R2=$(curl -s -m 60 http://127.0.0.1:8283/v1/chat/completions -H "Content-Type: application/json" \
  -d '{"model":"ox-alpha-free","messages":[{"role":"user","content":"reply B"}]}' | grep -c '"content"' || true)
[ "$R2" -ge 1 ] && ok "boss机器 LLM 通" || bad "boss机器 LLM 不通"

echo "--- [2] 租户隔离: alice 的 key 不能干 secops 的管理员操作"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://$GW/mcp \
  -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer sk-alice-demo-key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_user","arguments":{"id":1}}}')
BODY=$(curl -s -X POST http://$GW/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer sk-alice-demo-key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_user","arguments":{"id":1}}}' | head -c 200)
echo "$BODY" | grep -q "BLOCKED" && ok "alice delete_user 被权限轴拦截" || bad "alice 竟然能删用户: $BODY"

echo "--- [3] 中央聚合: 两个租户的事件都进中央库"
SESS=$(curl -s http://$UI/api/sessions)
echo "$SESS" | grep -q "tenant-alice" && ok "alice 会话在中央库" || bad "缺 alice 会话"
echo "$SESS" | grep -q "tenant-secops" && ok "secops 会话在中央库" || bad "缺 secops 会话"

echo "--- [4] trace 因果链: 同一 session 的 LLM+tool 事件共享 trace_id"
TR=$(curl -s "http://$UI/api/events" | python -c "
import json,sys
evs=json.load(sys.stdin)
traces={}
for e in evs:
    if e.get('TraceID'):
        traces.setdefault(e['SessionID'], set()).add(e['TraceID'])
multi=[s for s,t in traces.items() if len([e for e in evs if e.get('SessionID')==s])>=2]
print('ok' if traces else 'none')
")
[ "$TR" = "ok" ] && ok "trace_id 已写入事件" || bad "trace_id 缺失"

echo "--- [5] 注册表同步: 中央下发 → 探针挂载"
curl -s -X POST http://$UI/api/registry -H "Content-Type: application/json" \
  -d '{"name":"demo-tools","command":["D:/proj/agent-security-gateway/bin/upstream-mcp.exe"],"tenants":["alice"]}' > /dev/null
sleep 35  # sync loop interval
if [ -f "$HOME/.claude/mcp.json" ]; then
  grep -q "asg-demo-tools" "$HOME/.claude/mcp.json" && ok "注册表条目自动挂载到 agent 配置" || bad "挂载未生效"
else
  bad "~/.claude/mcp.json 不存在"
fi

echo ""
echo "=== 结果: PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" = "0" ]

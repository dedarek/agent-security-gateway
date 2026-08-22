#!/usr/bin/env bash
# 分步验证剩余项: 中央聚合 / trace / 注册表挂载
cd "$(dirname "$0")/.."
GW=127.0.0.1:8090
PASS=0; FAIL=0
ok(){ echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad(){ echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

echo "--- [3] 中央聚合"
SESS=$(curl -s http://$GW/api/sessions)
echo "$SESS" | grep -q "tenant-alice" && ok "alice 会话在中央库" || bad "缺 alice 会话: $SESS" 
echo "$SESS" | grep -q "tenant-secops" && ok "secops 会话在中央库" || bad "缺 secops 会话"

echo "--- [4] trace_id"
TR=$(curl -s "http://$GW/api/events" | python -c "
import json,sys
evs=json.load(sys.stdin)
has=any(e.get('trace_id') for e in evs)
print('ok' if has else 'none')
")
[ "$TR" = "ok" ] && ok "trace_id 已写入事件" || bad "trace_id 缺失"

echo "--- [5] 注册表同步挂载 (等待sync周期35秒)"
sleep 36
if [ -f "$HOME/.claude/mcp.json" ]; then
  grep -q "asg-demo-tools" "$HOME/.claude/mcp.json" && ok "注册表条目自动挂载" || bad "挂载未生效: $(cat $HOME/.claude/mcp.json | head -c 200)"
else
  bad "~/.claude/mcp.json 不存在"
fi

echo ""
echo "=== 结果: PASS=$PASS FAIL=$FAIL ==="

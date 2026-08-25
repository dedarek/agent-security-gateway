#!/usr/bin/env bash
# 启动全栈用于测试
cd /d/proj/agent-security-gateway
# 杀掉旧进程
for PORT in 8080 8090 8181; do
  PID=$(netstat -ano | grep ":$PORT" | grep LISTENING | awk '{print $5}' | head -n 1)
  [ -n "$PID" ] && powershell -Command "Stop-Process -Id $PID -Force" 2>/dev/null
done
sleep 2
# 清理旧数据
rm -f data/events.jsonl connect-events.jsonl* 2>/dev/null
# 启动
./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml &
sleep 3
./bin/asg-connect.exe serve -config connect.yaml &
sleep 3
echo "=== ALL UP ==="
curl -s http://127.0.0.1:8080/healthz && echo " gw:8080"
curl -s http://127.0.0.1:8181/healthz && echo " probe:8181"
curl -s -o /dev/null -w "ui: %{http_code}" http://127.0.0.1:8090/ && echo ""

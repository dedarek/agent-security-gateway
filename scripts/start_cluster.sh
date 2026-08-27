#!/usr/bin/env bash
# 启动中央网关+两个探针 (多机模拟)
cd "$(dirname "$0")/.."
./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml &
# The console reverse-proxies /explorer/ to Semantica on :8091.
bash scripts/run_explorer.sh &
for i in {1..20}; do
  if curl -fsS http://127.0.0.1:8091/api/health >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
./bin/asg-connect.exe serve -config D:/tools/tmp/probe-xiaoming.yaml &
./bin/asg-connect.exe serve -config D:/tools/tmp/probe-boss.yaml &
sleep 3
echo "all up"

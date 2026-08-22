#!/usr/bin/env bash
# 启动中央网关+两个探针 (多机模拟)
cd "$(dirname "$0")/.."
./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml &
sleep 2
./bin/asg-connect.exe serve -config D:/tools/tmp/probe-xiaoming.yaml &
./bin/asg-connect.exe serve -config D:/tools/tmp/probe-boss.yaml &
sleep 3
echo "all up"

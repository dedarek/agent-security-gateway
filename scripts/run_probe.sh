#!/usr/bin/env bash
cd /d/proj/agent-security-gateway
# 从 .env 读真实key
KEY=""
for line in $(grep -E "^OPENCODE_GO_API_KEY=" "$HOME/AppData/Local/hermes/.env" 2>/dev/null); do
  KEY="${line#OPENCODE_GO_API_KEY=}"
done

# Lenovo key 从 connect.yaml 提取(它是JWT不是zen key, 不会被redact)
LKEY=$(grep "api_key:" connect.yaml | grep -v "ENV\|OPENCODE" | head -1 | sed 's/.*api_key: "//;s/"//')

export OPENCODE_GO_API_KEY="$KEY"
exec ./bin/asg-connect.exe serve -config connect.yaml

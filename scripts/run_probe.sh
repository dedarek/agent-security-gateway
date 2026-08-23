#!/usr/bin/env bash
cd /d/proj/agent-security-gateway
KEY=""
for line in $(grep -E "^OPENCODE_GO_API_KEY=" "$HOME/AppData/Local/hermes/.env" 2>/dev/null); do
  KEY="${line#OPENCODE_GO_API_KEY=}"
done
export OPENCODE_GO_API_KEY="$KEY"
exec ./bin/asg-connect.exe serve -config connect.yaml

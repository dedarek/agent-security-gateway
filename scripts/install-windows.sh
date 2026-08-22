#!/usr/bin/env bash
# ASG Windows installer: builds binaries, registers gateway as a scheduled task
# (auto-start at logon), installs the probe next to the user's agents.
set -e
cd "$(dirname "$0")/.."

echo "[1/4] building..."
go build -o bin/gateway.exe ./cmd/gateway
go build -o bin/asg-connect.exe ./cmd/asg-connect
go build -o bin/upstream-mcp.exe ./cmd/upstream-mcp

echo "[2/4] registering gateway auto-start (scheduled task 'ASG-Gateway')"
powershell -Command "
\$action = New-ScheduledTaskAction -Execute '$PWD/bin/gateway.exe' -Argument 'serve -config $PWD/deploy/config.dev.yaml' -WorkingDirectory '$PWD'
\$trigger = New-ScheduledTaskTrigger -AtLogOn
Register-ScheduledTask -TaskName 'ASG-Gateway' -Action \$action -Trigger \$trigger -Force
" || echo "  (task registration skipped — start gateway manually if needed)"

echo "[3/4] probe config"
if [ ! -f connect.yaml ]; then
  cp deploy/connect.example.yaml connect.yaml
  echo "  created connect.yaml — edit providers.api_key with YOUR model key"
fi

echo "[4/4] done. Start manually with:"
echo "  bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml"
echo "  bin/asg-connect.exe serve -config connect.yaml"
echo "  bin/asg-connect.exe init -app claude-code   # route your agent via the probe"

#!/usr/bin/env bash
cd /d/proj/agent-security-gateway
exec ./bin/gateway.exe serve -config deploy/config.dev.yaml -tenants deploy/tenants.yaml

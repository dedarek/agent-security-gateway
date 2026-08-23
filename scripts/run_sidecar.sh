#!/usr/bin/env bash
cd /d/proj/agent-security-gateway/intelligence/analyzer
exec python sidecar.py --policy policy.iv --port 8901

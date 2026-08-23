#!/usr/bin/env bash
cd /d/proj/semantica
export SEMANTICA_API_KEY="asg-explorer-key"
export SEMANTICA_ALLOW_UNAUTHENTICATED=1
exec python -m semantica.explorer --graph D:/tools/tmp/asg_graph.json --port 8091 --no-browser

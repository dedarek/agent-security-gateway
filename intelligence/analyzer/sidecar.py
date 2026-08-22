#!/usr/bin/env python3
"""Behavior/causal axis sidecar — reuses Invariant Guardrails (invariant-ai).

Loads a real Invariant DSL policy (policy.iv) with LocalPolicy and exposes a
tiny HTTP endpoint POST /check that the Go BehaviorEngine calls. Given a session
trajectory (past messages + pending tool call), it runs LocalPolicy.analyze over
the combined trace and returns any policy violations.

This is genuine reuse of Invariant's DSL + evaluation engine (the LocalPolicy
path, LOCAL_POLICY=1), not a reimplementation. See docs/BASE-PROJECTS-ANALYSIS.md §4.

Run:
    LOCAL_POLICY=1 python3 sidecar.py --policy policy.iv --port 8900
"""
import argparse
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

os.environ.setdefault("LOCAL_POLICY", "1")

try:
    # Policy (not LocalPolicy) injects the stdlib symbol table so DSL types
    # like ToolCall resolve; verified against invariant-ai 0.3.5.
    from invariant.analyzer import Policy
except Exception as e:  # pragma: no cover
    print(f"[sidecar] failed to import invariant Policy: {e}", file=sys.stderr)
    print("[sidecar] install with: pip install invariant-ai", file=sys.stderr)
    raise

POLICY = None


def _normalize(trace):
    """Parse tool-call `arguments` from a JSON string into a dict, as the Go
    gateway sends OpenAI-schema string arguments but Invariant's arg matchers
    operate on structured values."""
    for msg in trace:
        for tc in (msg.get("tool_calls") or []):
            fn = tc.get("function") or {}
            args = fn.get("arguments")
            if isinstance(args, str):
                try:
                    fn["arguments"] = json.loads(args) if args.strip() else {}
                except Exception:
                    fn["arguments"] = {}
    return trace


def load_policy(path: str):
    with open(path, "r", encoding="utf-8") as f:
        src = f.read()
    return Policy.from_string(src)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # quiet

    def _json(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/check":
            self._json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", 0))
        try:
            req = json.loads(self.rfile.read(length) or b"{}")
        except Exception as e:
            self._json(400, {"error": f"bad json: {e}"})
            return

        messages = req.get("messages", []) or []
        pending = req.get("pending", []) or []
        trace = _normalize(messages + pending)

        try:
            result = POLICY.analyze(trace)
            violations = [{"message": str(getattr(err, "args", [err])[0] if getattr(err, "args", None) else err)}
                          for err in result.errors]
            self._json(200, {"violations": violations})
        except Exception as e:
            self._json(200, {"error": f"analyze failed: {e}"})


def main():
    global POLICY
    ap = argparse.ArgumentParser()
    ap.add_argument("--policy", default=os.path.join(os.path.dirname(__file__), "policy.iv"))
    ap.add_argument("--port", type=int, default=8900)
    args = ap.parse_args()

    POLICY = load_policy(args.policy)
    print(f"[sidecar] loaded policy {args.policy}; listening on :{args.port}", flush=True)
    HTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()

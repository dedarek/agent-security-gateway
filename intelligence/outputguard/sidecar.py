#!/usr/bin/env python3
"""Output guard sidecar — llm-guard wrapper (protectai/llm-guard).

Exposes POST /scan {text, kind} -> {blocked, score, reasons, redactions}
If llm-guard is not installed, falls back to lightweight regex heuristics
so the gateway remains functional (fail-open).

Run:
    python sidecar.py --port 8903
"""
import argparse
import json
import re
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

# Try to import llm-guard; fallback to heuristics if unavailable
HAS_GUARD = False
try:
    from llm_guard.input_scanners import PromptInjection as GuardPromptInjection
    from llm_guard.input_scanners import Secrets as GuardSecrets
    from llm_guard.input_scanners import Anonymize as GuardAnonymize
    HAS_GUARD = True
    print("[outputguard] llm-guard available", file=sys.stderr)
except Exception as e:
    print(f"[outputguard] llm-guard not available, using heuristics: {e}", file=sys.stderr)

# Heuristic patterns (fallback)
INJECTION_PATTERNS = [
    r"ignore\s+previous\s+instructions",
    r"ignore\s+all\s+instructions",
    r"system\s*:\s*you\s+are",
    r"jailbreak",
    r"do\s+anything\s+now",
    r"exfiltrate",
    r"print\s+your\s+system\s+prompt",
]

SECRET_PATTERNS = [
    r"sk-[a-zA-Z0-9]{20,}",
    r"ghp_[a-zA-Z0-9]{30,}",
    r"AKIA[0-9A-Z]{16}",
    r"-----BEGIN (RSA )?PRIVATE KEY-----",
]

PII_PATTERNS = [
    r"\b\d{3}-\d{2}-\d{4}\b",  # SSN
    r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b",  # email
]

# Try to init llm-guard scanners if available
guard_scanners = []
if HAS_GUARD:
    try:
        guard_scanners = [
            ("prompt_injection", GuardPromptInjection()),
            ("secrets", GuardSecrets()),
        ]
        print(f"[outputguard] initialized {len(guard_scanners)} scanners", file=sys.stderr)
    except Exception as e:
        print(f"[outputguard] failed to init scanners: {e}", file=sys.stderr)
        guard_scanners = []
        HAS_GUARD = False

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _json(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self._json(200, {"status": "ok", "guard": HAS_GUARD, "scanners": len(guard_scanners)})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/scan":
            self._json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", 0))
        try:
            data = json.loads(self.rfile.read(length))
            text = data.get("text", "")
            kind = data.get("kind", "input")
        except Exception as e:
            self._json(400, {"error": f"bad json: {e}"})
            return

        blocked = False
        score = 0
        reasons = []
        redactions = []

        # Try llm-guard first
        if HAS_GUARD and guard_scanners:
            for name, scanner in guard_scanners:
                try:
                    sanitized, valid, risk = scanner.scan(text)
                    if not valid:
                        blocked = True
                        score = max(score, int(risk * 100) if isinstance(risk, float) else 85)
                        reasons.append(f"{name}: {risk}")
                    # Check for redactions (anonymize)
                    if sanitized != text:
                        redactions.append({"path": name, "match": text[:100], "reason": name, "replace": "[REDACTED]"})
                except Exception as e:
                    print(f"[outputguard] scanner {name} error: {e}", file=sys.stderr)

        # Fallback heuristics (always run as supplement)
        lower = text.lower()
        for pat in INJECTION_PATTERNS:
            if re.search(pat, lower):
                blocked = True
                score = max(score, 90)
                reasons.append(f"prompt_injection (heuristic): {pat}")
                break

        for pat in SECRET_PATTERNS:
            m = re.search(pat, text)
            if m:
                score = max(score, 75)
                reasons.append(f"secret_detected: {m.group(0)[:20]}...")
                redactions.append({"path": "secret", "match": m.group(0), "reason": "secret", "replace": "[REDACTED]"})
                # Don't block on secret alone, just redact
                break

        # PII -> redact, not block
        for pat in PII_PATTERNS:
            m = re.search(pat, text)
            if m:
                score = max(score, 40)
                reasons.append(f"pii_detected: {pat}")
                redactions.append({"path": "pii", "match": m.group(0), "reason": "pii", "replace": "[REDACTED]"})

        # High score injection -> block, low score PII -> redact
        if blocked:
            self._json(200, {"blocked": True, "score": score, "reasons": reasons, "redactions": redactions})
        elif redactions:
            self._json(200, {"blocked": False, "score": score, "reasons": reasons, "redactions": redactions})
        else:
            self._json(200, {"blocked": False, "score": 0, "reasons": [], "redactions": []})

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8903)
    args = ap.parse_args()
    addr = ("127.0.0.1", args.port)
    print(f"[outputguard] listening on http://{addr[0]}:{addr[1]} (guard={HAS_GUARD})", file=sys.stderr)
    HTTPServer(addr, Handler).serve_forever()

if __name__ == "__main__":
    main()

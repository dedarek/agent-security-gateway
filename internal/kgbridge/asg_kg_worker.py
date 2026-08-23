#!/usr/bin/env python3
"""ASG Semantica worker — the KG/semantic side of the security graph.

Bridges the Go gateway to Semantica:
  POST /ingest  {entities:[], relationships:[]}   → KnowledgeGraph + provenance
  POST /events  {texts:[], event_ids:[]}           → fastembed index
  GET  /search?query=&top_k=                      → semantic similarity hits
  POST /ask    {question}                         → KG-grounded LLM answer (free model)

All embedding is LOCAL (fastembed ONNX, zero API cost). The LLM call goes
through the ASG probe (ox-alpha-free), never a paid provider.
"""
import argparse
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

KG_ENTITIES = []
KG_RELATIONSHIPS = []

EMBEDDER = None
LLM = None
INDEX_TEXTS = []
INDEX_VECS = []
INDEX_IDS = []


def _init_semantica(semantica_path):
    if semantica_path:
        sys.path.insert(0, semantica_path)
    global EMBEDDER, LLM
    from semantica.embeddings import TextEmbedder
    from semantica.llms import OpenAI
    EMBEDDER = TextEmbedder(method="fastembed")
    probe = os.environ.get("ASG_PROBE_URL", "http://127.0.0.1:8181/v1")
    key = os.environ.get("ASG_PROBE_KEY", "dummy")
    LLM = OpenAI(model="ox-alpha-free", api_key=key, base_url=probe)
    print("[worker] semantica ready (fastembed local; llm via probe)", flush=True)


def _ingest(payload):
    ents = payload.get("entities") or []
    rels = payload.get("relationships") or []
    for e in ents:
        if e not in KG_ENTITIES:
            KG_ENTITIES.append(e)
    for r in rels:
        if r not in KG_RELATIONSHIPS:
            KG_RELATIONSHIPS.append(r)
    return {"entities": len(KG_ENTITIES), "relationships": len(KG_RELATIONSHIPS)}


def _index_events(payload):
    texts = payload.get("texts") or []
    ids = payload.get("event_ids") or []
    vecs = EMBEDDER.embed_batch(texts) if texts else []
    for t, v, i in zip(texts, vecs, ids):
        INDEX_TEXTS.append(t)
        INDEX_VECS.append(list(v))
        INDEX_IDS.append(i)
    return {"indexed": len(texts), "total": len(INDEX_TEXTS)}


def _search(payload):
    q = payload.get("query", "")
    top_k = int(payload.get("top_k", 5))
    if not INDEX_TEXTS:
        return {"hits": []}
    import numpy as np
    qv = EMBEDDER.embed_text(q)
    sims = []
    for t, v, i in zip(INDEX_TEXTS, INDEX_VECS, INDEX_IDS):
        v = np.array(v)
        sim = float(np.dot(qv, v) / (np.linalg.norm(qv) * np.linalg.norm(v) + 1e-9))
        sims.append((sim, t, i))
    sims.sort(key=lambda x: -x[0])
    hits = [{"score": round(s, 4), "text": t[:200], "event_id": i}
            for s, t, i in sims[:top_k]]
    return {"hits": hits}


def _ask(payload):
    q = payload.get("question", "")
    kg_ctx = json.dumps({"entities": KG_ENTITIES[-40:], "relationships": KG_RELATIONSHIPS[-40:]},
                        default=str)
    prompt = f"Security knowledge graph:\n{kg_ctx}\n\nQuestion: {q}\nAnswer in one short paragraph."
    ans = LLM.generate(prompt)
    return {"answer": str(ans)[:1000]}


import os  # noqa: E402


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _json(self, code, obj):
        body = json.dumps(obj, default=str).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/health"):
            self._json(200, {"status": "ok", "entities": len(KG_ENTITIES),
                             "indexed": len(INDEX_TEXTS)})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        ln = int(self.headers.get("Content-Length", 0))
        try:
            payload = json.loads(self.rfile.read(ln) or b"{}")
        except Exception as e:
            self._json(400, {"error": str(e)})
            return
        try:
            if self.path == "/ingest":
                self._json(200, _ingest(payload))
            elif self.path == "/events":
                self._json(200, _index_events(payload))
            elif self.path == "/search":
                self._json(200, _search(payload))
            elif self.path == "/ask":
                self._json(200, _ask(payload))
            else:
                self._json(404, {"error": "not found"})
        except Exception as e:
            self._json(500, {"error": str(e)})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8902)
    ap.add_argument("--semantica-path", default="")
    args = ap.parse_args()

    _init_semantica(args.semantica_path)
    HTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()

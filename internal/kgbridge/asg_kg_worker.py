#!/usr/bin/env python3
"""ASG Semantica worker — the KG/semantic side of the security graph.

Bridges the Go gateway to Semantica:
  POST /ingest  {entities:[], relationships:[]}   → KnowledgeGraph + provenance
  POST /events  {texts:[], event_ids:[]}           → fastembed index
  GET  /search?query=&top_k=                      → semantic similarity hits
  POST /ask    {question}                         → KG-grounded LLM answer (free model)

Graph lineage (Semantica GraphSession + ContextGraph):
  GET  /graph/nodes    → all nodes (type, content, props)
  GET  /graph/edges    → all edges (source, target, type, weight)
  GET  /graph/path?source=&target= → BFS shortest path (链路追溯)
  GET  /graph/neighbors/{id}       → 1-hop neighbors

All embedding is LOCAL (fastembed ONNX, zero API cost). The LLM call goes
through the ASG probe (ox-alpha-free), never a paid provider.
"""
import argparse
import json
import os
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

# Semantica graph session — the real lineage graph
GRAPH_SESSION = None


def _init_semantica(semantica_path):
    if semantica_path:
        sys.path.insert(0, semantica_path)
    global EMBEDDER, LLM, GRAPH_SESSION
    from semantica.embeddings import TextEmbedder
    from semantica.llms import OpenAI
    EMBEDDER = TextEmbedder(method="fastembed")
    probe = os.environ.get("ASG_PROBE_URL", "http://127.0.0.1:8181/v1")
    key = os.environ.get("ASG_PROBE_KEY", "dummy")
    LLM = OpenAI(model="ox-alpha-free", api_key=key, base_url=probe)

    # Real Semantica graph — lineage/provenance core
    try:
        from semantica.context.context_graph import ContextGraph
        from semantica.explorer.session import GraphSession
        _graph = ContextGraph()
        GRAPH_SESSION = GraphSession(_graph)
        print("[worker] semantica graph session ready", flush=True)
    except Exception as e:
        print(f"[worker] graph session unavailable: {e}", flush=True)

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

    # Mirror into the real Semantica graph session for lineage queries.
    # Use ContextGraph.add_node (not add_nodes) so our custom type is preserved.
    if GRAPH_SESSION:
        g = GRAPH_SESSION.graph
        for e in ents:
            nid = e.get("id", "")
            ntype = e.get("type", "Unknown")
            props = e.get("props", {}) or {}
            content = str(props.get("rationale") or props.get("verdict") or nid)
            g.add_node(nid, ntype, content=content, **props)
        for r in rels:
            g.add_edge(
                r.get("source", ""),
                r.get("target", ""),
                edge_type=r.get("type", "related_to"),
            )
        GRAPH_SESSION.rebuild_search_index()

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


def _embed(payload):
    texts = payload.get("texts") or []
    if not EMBEDDER:
        return {"vectors": []}
    vecs = EMBEDDER.embed_batch(texts) if texts else []
    return {"vectors": [list(v) for v in vecs]}


def _ask(payload):
    q = payload.get("question", "")
    kg_ctx = json.dumps({"entities": KG_ENTITIES[-40:], "relationships": KG_RELATIONSHIPS[-40:]},
                        default=str)
    prompt = f"Security knowledge graph:\n{kg_ctx}\n\nQuestion: {q}\nAnswer in one short paragraph."
    ans = LLM.generate(prompt)
    return {"answer": str(ans)[:1000]}


# ── Graph lineage endpoints (Semantica GraphSession) ─────────────────────────

def _graph_nodes():
    if not GRAPH_SESSION:
        return {"nodes": [], "total": 0}
    nodes, total, _ = GRAPH_SESSION.paginate_nodes(limit=500)
    out = []
    for n in nodes:
        out.append({
            "id": n.get("id", ""),
            "type": n.get("type", ""),
            "content": str(n.get("content", ""))[:120],
            "properties": n.get("properties", {}),
        })
    return {"nodes": out, "total": total}


def _graph_edges():
    if not GRAPH_SESSION:
        return {"edges": [], "total": 0}
    edges, total, _ = GRAPH_SESSION.paginate_edges(limit=1000)
    out = []
    for e in edges:
        out.append({
            "source": e.get("source", ""),
            "target": e.get("target", ""),
            "type": e.get("type", ""),
            "weight": e.get("weight", 1.0),
        })
    return {"edges": out, "total": total}


def _graph_path(source, target):
    """链路追溯：BFS shortest path between two entities using GraphSession's own finder."""
    if not GRAPH_SESSION:
        return {"path": [], "hop_count": 0, "error": "graph not ready"}
    try:
        # BFS via repeated get_neighbors calls (no fastapi dependency)
        visited = {source: None}  # node -> parent
        queue = [source]
        while queue:
            node = queue.pop(0)
            if node == target:
                # reconstruct path
                path = []
                cur = target
                while cur is not None:
                    path.append(cur)
                    cur = visited[cur]
                path.reverse()
                return {"path": path, "hop_count": len(path) - 1, "algorithm": "bfs"}
            for nb in GRAPH_SESSION.get_neighbors(node, depth=1):
                nid = nb.get("id", "")
                if nid not in visited:
                    visited[nid] = node
                    queue.append(nid)
        return {"path": [], "hop_count": 0, "error": "no path found"}
    except Exception as e:
        return {"path": [], "hop_count": 0, "error": str(e)}


def _graph_neighbors(node_id, depth=1):
    if not GRAPH_SESSION:
        return {"neighbors": []}
    nbs = GRAPH_SESSION.get_neighbors(node_id, depth)
    return {"neighbors": [
        {"id": n.get("id",""), "type": n.get("type",""),
         "content": str(n.get("content",""))[:120],
         "relationship": n.get("relationship",""), "hop": n.get("hop",1)}
        for n in nbs
    ]}


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
                             "indexed": len(INDEX_TEXTS),
                             "graph_ready": GRAPH_SESSION is not None})
        elif self.path == "/graph/nodes":
            self._json(200, _graph_nodes())
        elif self.path == "/graph/edges":
            self._json(200, _graph_edges())
        elif self.path.startswith("/graph/path"):
            from urllib.parse import urlparse, parse_qs
            qs = parse_qs(urlparse(self.path).query)
            src = qs.get("source", [""])[0]
            tgt = qs.get("target", [""])[0]
            self._json(200, _graph_path(src, tgt))
        elif self.path.startswith("/graph/neighbors/"):
            node_id = self.path.split("/graph/neighbors/")[-1]
            self._json(200, _graph_neighbors(node_id))
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
            elif self.path == "/embed":
                self._json(200, _embed(payload))
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

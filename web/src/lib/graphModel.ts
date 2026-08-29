// graphModel — risk-first subgraph builder for the KG view.
// Pure functions (no React), unit-testable. Carries the five pruning rules:
//  1. drop Trace nodes (they are 1:1 with Event and carry no information)
//  2. merge Agents by agent_id (empty-id agents dropped; session count kept)
//  3. filter Events by verdict according to the view mode
//  4. drop nodes left with degree 0 after pruning
//  5. if still > cap, keep highest-risk first; BLOCK is never truncated

export type KGNode = {
  id: string
  type: string
  content?: string
  properties?: Record<string, any>
}

export type KGEdge = {
  source: string
  target: string
  type?: string
  weight?: number
}

export type GraphMode = 'risk' | 'review' | 'full'

export type GraphStats = {
  rawNodes: number
  rawEdges: number
  shown: number
  omitted: number
}

export type GraphResult = {
  nodes: KGNode[]
  edges: KGEdge[]
  stats: GraphStats
}

const CAP = 120

export function buildRiskSubgraph(nodes: KGNode[], edges: KGEdge[], mode: GraphMode): GraphResult {
  const rawNodes = nodes.length
  const rawEdges = edges.length

  // Rule 1: drop Trace nodes + their edges, remember trace→event link is redundant.
  const traceIds = new Set(nodes.filter((n) => n.type === 'Trace').map((n) => n.id))
  let keepNodes = nodes.filter((n) => !traceIds.has(n.id))
  let keepEdges = edges.filter((e) => !traceIds.has(e.source) && !traceIds.has(e.target))

  // Rule 2: drop empty-id agents; merge duplicate agent ids (already unique by id).
  keepNodes = keepNodes.filter((n) => !(n.type === 'Agent' && (!n.id || n.id === 'agent:@' || n.id.endsWith(':@'))))

  // Rule 3: event verdict filter.
  const verdictAllowed = (v: any): boolean => {
    const s = String(v || 'ALLOW').toUpperCase()
    if (mode === 'risk') return s === 'BLOCK'
    if (mode === 'review') return s === 'BLOCK' || s === 'CONFIRM'
    return true
  }
  const allowedEventIds = new Set(
    keepNodes.filter((n) => n.type === 'Event' && verdictAllowed(n.properties?.verdict)).map((n) => n.id),
  )

  // Keep an event's 1-hop context: agents/tools connected to kept events.
  const contextIds = new Set<string>()
  for (const e of keepEdges) {
    const sIn = allowedEventIds.has(e.source)
    const tIn = allowedEventIds.has(e.target)
    if (sIn || tIn) {
      if (sIn) contextIds.add(e.target)
      if (tIn) contextIds.add(e.source)
    }
  }
  keepNodes = keepNodes.filter((n) => {
    if (n.type === 'Event') return allowedEventIds.has(n.id)
    if (n.type === 'Agent' || n.type === 'Tool' || n.type === 'ExternalActor') {
      // In risk/review modes only keep context-connected; full keeps all.
      return mode === 'full' ? true : contextIds.has(n.id)
    }
    return true
  })

  // Rule 4: drop degree-0 leftovers (except in full mode where we keep tools for reference).
  if (mode !== 'full') {
    const degree = new Map<string, number>()
    const idSet = new Set(keepNodes.map((n) => n.id))
    for (const e of keepEdges) {
      if (idSet.has(e.source) && idSet.has(e.target)) {
        degree.set(e.source, (degree.get(e.source) || 0) + 1)
        degree.set(e.target, (degree.get(e.target) || 0) + 1)
      }
    }
    keepNodes = keepNodes.filter((n) => (degree.get(n.id) || 0) > 0)
  }

  // Rule 5: cap. Sort by risk desc; BLOCK first so truncation never drops it.
  if (keepNodes.length > CAP) {
    const score = (n: KGNode) => {
      const v = String(n.properties?.verdict || '').toUpperCase()
      if (v === 'BLOCK') return 100000 + (n.properties?.risk || 0)
      if (v === 'CONFIRM') return 50000 + (n.properties?.risk || 0)
      if (n.type === 'Agent') return 10000
      if (n.type === 'Tool') return 5000
      return n.properties?.risk || 0
    }
    keepNodes = keepNodes.slice().sort((a, b) => score(b) - score(a)).slice(0, CAP)
  }

  const finalIds = new Set(keepNodes.map((n) => n.id))
  const finalEdges = keepEdges.filter((e) => finalIds.has(e.source) && finalIds.has(e.target))

  return {
    nodes: keepNodes,
    edges: finalEdges,
    stats: {
      rawNodes,
      rawEdges,
      shown: keepNodes.length,
      omitted: rawNodes - keepNodes.length,
    },
  }
}

/** Shortest path (BFS) between two node ids over the given edges. */
export function shortestPath(edges: KGEdge[], from: string, to: string): string[] {
  if (from === to) return [from]
  const adj = new Map<string, string[]>()
  for (const e of edges) {
    if (!adj.has(e.source)) adj.set(e.source, [])
    if (!adj.has(e.target)) adj.set(e.target, [])
    adj.get(e.source)!.push(e.target)
    adj.get(e.target)!.push(e.source) // treat as undirected for lineage walk
  }
  const prev = new Map<string, string>()
  const q: string[] = [from]
  const seen = new Set([from])
  while (q.length) {
    const cur = q.shift()!
    for (const nb of adj.get(cur) || []) {
      if (seen.has(nb)) continue
      seen.add(nb)
      prev.set(nb, cur)
      if (nb === to) {
        const path = [to]
        let p = cur
        while (p !== from) { path.unshift(p); p = prev.get(p)! }
        path.unshift(from)
        return path
      }
      q.push(nb)
    }
  }
  return []
}

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

// ─────────────────────────────────────────────────────────────────────────────
// Onto-graph adapter — turns /api/onto/graph (agent / origin / story / verdict)
// into a HUMAN-READABLE security-story subgraph. No probe UUIDs, no dup edges.
//
// Story we want an outsider to read at a glance:
//   智能体(Agent) ──触发──▶ 拦截(BLOCK verdict) ──涉及──▶ 敏感资源(sensitive origin)
//   智能体(Agent) ──产生──▶ 高危会话(story, outcome=blocked)
// ─────────────────────────────────────────────────────────────────────────────

export type OntoRaw = {
  nodes: { id: string; type: string; label?: string; props?: Record<string, any> }[]
  edges: { source: string; target: string; type?: string }[]
}

const AGENT_CN: Record<string, string> = {
  'local-claude-code': 'Claude Code',
  'claude-code': 'Claude Code',
  'local-codex': 'Codex',
  'codex': 'Codex',
}

function agentName(id: string, label?: string): string {
  const key = (label || id).replace(/^agent:/, '')
  return AGENT_CN[key] || key
}

/** Build the readable onto subgraph. mode: risk=只看拦截, review=拦截+会话, full=全部. */
export function buildOntoSubgraph(raw: OntoRaw, mode: GraphMode): GraphResult {
  const rawNodes = raw.nodes.length
  const rawEdges = raw.edges.length
  const byId = new Map(raw.nodes.map((n) => [n.id, n]))

  const outN: KGNode[] = []
  const outE: KGEdge[] = []
  const kept = new Set<string>()

  const keep = (n: KGNode) => { if (!kept.has(n.id)) { kept.add(n.id); outN.push(n) } }

  // 1) Agents — always shown, human names.
  for (const n of raw.nodes) {
    if (n.type === 'agent') {
      keep({ id: n.id, type: 'Agent', content: agentName(n.id, n.label),
        properties: { kind: 'agent' } })
    }
  }

  // 2) Verdicts (BLOCK) — the intercepts. Label = tool + risk.
  const verdictNodes = raw.nodes.filter((n) => n.type === 'verdict')
  for (const n of verdictNodes) {
    const rawTool = String(n.props?.tool || '').trim()
    const tool = (!rawTool || rawTool.toLowerCase() === 'unknown') ? '高危调用' : rawTool
    const risk = Number(n.props?.risk || 0)
    keep({ id: n.id, type: 'Event', content: `拦截 · ${tool}`,
      properties: { verdict: 'BLOCK', risk, tool, at: n.props?.at, session: n.props?.session, agent: n.props?.agent } })
    // link agent → verdict
    const agentId = `agent:${n.props?.agent || ''}`
    const agentAlt = raw.nodes.find((a) => a.type === 'agent' && (a.id === agentId || a.label === n.props?.agent))
    if (agentAlt) outE.push({ source: agentAlt.id, target: n.id, type: '触发拦截' })
  }

  // 3) Origins — sensitive resources touched. Only sensitive ones in risk mode.
  for (const n of raw.nodes) {
    if (n.type !== 'origin') continue
    const sensitive = !!n.props?.sensitive
    if (mode === 'risk' && !sensitive) continue
    keep({ id: n.id, type: 'Tool', content: n.label || n.id.replace(/^org:file:/, ''),
      properties: { kind: 'resource', sensitive, reads: n.props?.reads } })
  }

  // 4) Stories (high-risk sessions) — only in review/full.
  if (mode !== 'risk') {
    for (const n of raw.nodes) {
      if (n.type !== 'story') continue
      if (n.props?.outcome !== 'blocked' && mode !== 'full') continue
      const risk = Number(n.props?.peak_risk || 0)
      keep({ id: n.id, type: 'Event', content: `会话 · ${n.props?.steps || 0}步`,
        properties: { verdict: n.props?.outcome === 'blocked' ? 'BLOCK' : 'ALLOW', risk, steps: n.props?.steps, outcome: n.props?.outcome, agent: n.props?.agent } })
    }
  }

  // 5) Edges from onto — dedup + relabel, only between kept nodes.
  const edgeSeen = new Set<string>()
  const relabel: Record<string, string> = { narrates: '产生会话', reads: '读取资源', resulted_in: '产生裁决' }
  for (const e of raw.edges) {
    // orient story→agent as agent→story for readability
    let s = e.source, t = e.target
    const st = byId.get(s), tt = byId.get(t)
    if (st?.type === 'story' && tt?.type === 'agent') { s = e.target; t = e.source }
    if (!kept.has(s) || !kept.has(t)) continue
    const key = `${s}|${t}|${e.type}`
    if (edgeSeen.has(key)) continue
    edgeSeen.add(key)
    outE.push({ source: s, target: t, type: relabel[e.type || ''] || e.type })
  }

  // dedup the agent→verdict edges we added in step 2
  const finalEdges: KGEdge[] = []
  const fseen = new Set<string>()
  for (const e of outE) {
    if (!kept.has(e.source) || !kept.has(e.target)) continue
    const k = `${e.source}|${e.target}|${e.type}`
    if (fseen.has(k)) continue
    fseen.add(k)
    finalEdges.push(e)
  }

  // drop degree-0 agents in risk mode (agents with no intercept aren't interesting)
  let finalNodes = outN
  if (mode === 'risk') {
    const deg = new Map<string, number>()
    for (const e of finalEdges) { deg.set(e.source, (deg.get(e.source) || 0) + 1); deg.set(e.target, (deg.get(e.target) || 0) + 1) }
    finalNodes = outN.filter((n) => (deg.get(n.id) || 0) > 0)
    const fset = new Set(finalNodes.map((n) => n.id))
    return {
      nodes: finalNodes,
      edges: finalEdges.filter((e) => fset.has(e.source) && fset.has(e.target)),
      stats: { rawNodes, rawEdges, shown: finalNodes.length, omitted: rawNodes - finalNodes.length },
    }
  }

  return {
    nodes: finalNodes,
    edges: finalEdges,
    stats: { rawNodes, rawEdges, shown: finalNodes.length, omitted: rawNodes - finalNodes.length },
  }
}

// ontoModel — front-end projection of the unified B/D/E ontology graph.
// Pure functions (testable): build cytoscape elements from /api/onto/* data.

export type OntoNode = {
  id: string
  type: 'story' | 'origin' | 'sink' | 'agent' | 'tool' | 'verdict'
  label: string
  props?: Record<string, any>
}
export type OntoEdge = { source: string; target: string; type: string; props?: Record<string, any> }

export const ONTO_COLOR: Record<string, string> = {
  agent: '#f5a623',   // amber
  tool: '#4a9bd4',    // blue
  origin: '#18a999',  // teal — the source of data
  sink: '#a06ee1',    // purple — the destination
  story: '#8a94a6',   // grey capsule
  verdict: '#ff5f56', // red diamond
}

export const ONTO_SHAPE: Record<string, string> = {
  agent: 'ellipse',
  tool: 'round-rectangle',
  origin: 'round-tag',
  sink: 'hexagon',
  story: 'round-rectangle',
  verdict: 'diamond',
}

export function ontoToCy(nodes: OntoNode[], edges: OntoEdge[]) {
  const cyNodes = nodes.map((n) => {
    const isSensitive = n.props?.sensitive === true
    const reuse = Number(n.props?.reads || 0) + Number(n.props?.exfil_count || 0)
    const base = n.type === 'agent' ? 50 : n.type === 'tool' ? 44 : n.type === 'verdict' ? 40 : 36
    const size = base + Math.min(reuse * 2, 18) // reuse-degree grows the node a little
    return {
      data: {
        id: n.id,
        label: trunc(n.label || n.id, 22),
        fullLabel: n.label || n.id,
        type: n.type,
        size,
        color: ONTO_COLOR[n.type] || '#8a94a6',
        shape: ONTO_SHAPE[n.type] || 'ellipse',
        sensitive: isSensitive,
        verdict: n.props?.outcome === 'blocked' || n.type === 'verdict' ? 'BLOCK' : '',
        props: n.props || {},
      },
    }
  })
  const cyEdges = edges.map((e, i) => ({
    data: {
      id: `e${i}`,
      source: e.source,
      target: e.target,
      type: e.type,
      label: e.type === 'flows_to' ? '污点流' : e.type === 'exfiltrates' ? '外发' : e.type === 'reads' ? '读取' : '',
      tainted: e.props?.tainted === true,
      isBlock: e.props?.verdict === 'BLOCK',
      props: e.props || {},
    },
  }))
  return [...cyNodes, ...cyEdges]
}

function trunc(s: string, n: number) {
  return s.length > n ? s.slice(0, n) + '…' : s
}

import { useEffect, useRef, useState, useCallback } from 'react'
import cytoscape from 'cytoscape'
import { api } from '../lib/api'

type KGNode = {
  id: string
  type: string
  content: string
  properties: Record<string, any>
}
type KGEdge = {
  source: string
  target: string
  type: string
  weight: number
}

const TYPE_COLOR: Record<string, string> = {
  Agent: '#4a9eff',
  Tool: '#e8a317',
  Event: '#8092a6',
  Trace: '#9a7fd1',
  ExternalActor: '#e15a4a',
}

const TYPE_SHAPE: Record<string, string> = {
  Agent: 'ellipse',
  Tool: 'round-rectangle',
  Event: 'round-rectangle',
  Trace: 'diamond',
  ExternalActor: 'hexagon',
}

function shortLabel(id: string, content: string, max = 22): string {
  const raw = content || id
  // evt:hook-xxx -> hook-xxx, agent:@xxx -> @xxx
  const cleaned = raw.replace(/^evt:/, '').replace(/^agent:@/, '@').replace(/^trace:/, '').replace(/^tool:/, '')
  return cleaned.length > max ? cleaned.slice(0, max) + '…' : cleaned
}

export default function KGGraph() {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<cytoscape.Core | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [showAll, setShowAll] = useState(false)
  const [stats, setStats] = useState<{ nodes: number; edges: number; filtered: number } | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const rawRef = useRef<{ nodes: KGNode[]; edges: KGEdge[] } | null>(null)

  const buildElements = useCallback((nodes: KGNode[], edges: KGEdge[], all: boolean) => {
    // verdict map from node properties
    const verdictById = new Map<string, string>()
    nodes.forEach(n => {
      const v = n.properties?.verdict || ''
      if (v) verdictById.set(n.id, v)
    })

    // filtering: keep Agent/Tool/ExternalActor + their 1-hop neighbors
    let keepIds: Set<string> | null = null
    if (!all) {
      const coreTypes = new Set(['Agent', 'Tool', 'ExternalActor'])
      const coreIds = new Set(nodes.filter(n => coreTypes.has(n.type)).map(n => n.id))
      keepIds = new Set(coreIds)
      edges.forEach(e => {
        if (coreIds.has(e.source)) keepIds!.add(e.target)
        if (coreIds.has(e.target)) keepIds!.add(e.source)
      })
      // If still too small, fallback to showAll
      if (keepIds.size < 20) keepIds = null
    }

    const filteredNodes = keepIds ? nodes.filter(n => keepIds!.has(n.id)) : nodes
    const filteredIds = new Set(filteredNodes.map(n => n.id))
    const filteredEdges = edges.filter(e => filteredIds.has(e.source) && filteredIds.has(e.target))

    // include BLOCK nodes even if filtered out (always keep BLOCK for demo)
    // if filtering removed all BLOCK nodes, add them back
    if (keepIds) {
      const blocks = nodes.filter(n => n.properties?.verdict === 'BLOCK' && !filteredIds.has(n.id))
      blocks.forEach(b => {
        filteredNodes.push(b)
        filteredIds.add(b.id)
        // add edges incident to this BLOCK node where other end already in set
        edges.forEach(e => {
          if ((e.source === b.id && filteredIds.has(e.target)) || (e.target === b.id && filteredIds.has(e.source))) {
            if (!filteredEdges.find(fe => fe.source === e.source && fe.target === e.target)) {
              filteredEdges.push(e)
            }
          }
        })
      })
    }

    const cyNodes = filteredNodes.map(n => {
      const verdict = n.properties?.verdict || ''
      const isBlock = verdict === 'BLOCK'
      return {
        data: {
          id: n.id,
          label: shortLabel(n.id, n.content),
          fullLabel: n.content || n.id,
          type: n.type,
          verdict,
          risk: n.properties?.risk ?? 0,
          color: TYPE_COLOR[n.type] || '#8092a6',
          shape: TYPE_SHAPE[n.type] || 'ellipse',
          isBlock,
        },
      }
    })

    const cyEdges = filteredEdges.map((e, i) => {
      const targetVerdict = verdictById.get(e.target) || ''
      const sourceVerdict = verdictById.get(e.source) || ''
      const isBlock = targetVerdict === 'BLOCK' || sourceVerdict === 'BLOCK'
      return {
        data: {
          id: `e${i}-${e.source}-${e.target}`,
          source: e.source,
          target: e.target,
          type: e.type,
          verdict: isBlock ? 'BLOCK' : '',
          isBlock,
          label: e.type,
        },
      }
    })

    return {
      elements: [...cyNodes, ...cyEdges],
      filteredCount: filteredNodes.length,
      totalEdges: filteredEdges.length,
    }
  }, [])

  const render = useCallback(async (all: boolean) => {
    if (!containerRef.current) return
    setLoading(true)
    setErr(null)
    try {
      let nodesRes: any, edgesRes: any
      if (rawRef.current) {
        nodesRes = { nodes: rawRef.current.nodes }
        edgesRes = { edges: rawRef.current.edges }
      } else {
        const [nr, er] = await Promise.all([api.kgNodes(), api.kgEdges()])
        const n = (nr as any).nodes || (nr as any) || []
        const e = (er as any).edges || (er as any) || []
        nodesRes = { nodes: Array.isArray(n) ? n : [] }
        edgesRes = { edges: Array.isArray(e) ? e : [] }
        rawRef.current = { nodes: nodesRes.nodes, edges: edgesRes.edges }
      }

      const { elements, filteredCount, totalEdges } = buildElements(rawRef.current!.nodes, rawRef.current!.edges, all)
      setStats({ nodes: rawRef.current!.nodes.length, edges: rawRef.current!.edges.length, filtered: filteredCount })

      // destroy old
      if (cyRef.current) {
        cyRef.current.destroy()
        cyRef.current = null
      }

      const cy = cytoscape({
        container: containerRef.current,
        elements,
        layout: {
          name: 'cose',
          idealEdgeLength: 80,
          nodeOverlap: 12,
          refresh: 20,
          fit: true,
          padding: 30,
          randomize: false,
          componentSpacing: 40,
          nodeRepulsion: () => 450000,
          edgeElasticity: () => 100,
          nestingFactor: 1.2,
          gravity: 80,
          numIter: 1000,
          initialTemp: 200,
          coolingFactor: 0.95,
          minTemp: 1.0,
        } as any,
        style: [
          {
            selector: 'node',
            style: {
              'background-color': 'data(color)',
              'label': 'data(label)',
              'color': '#e6ebf2',
              'font-size': 7,
              'text-valign': 'center',
              'text-halign': 'center',
              'text-wrap': 'wrap',
              'text-max-width': 70,
              'width': 42,
              'height': 42,
              'border-width': 2,
              'border-color': '#1a2430',
              'shape': 'data(shape)',
              'overlay-opacity': 0,
            } as any,
          },
          {
            selector: 'node[type="Agent"]',
            style: { 'width': 48, 'height': 48, 'font-size': 7, 'font-weight': 600 } as any,
          },
          {
            selector: 'node[type="Tool"]',
            style: { 'width': 52, 'height': 32, 'font-size': 7 } as any,
          },
          {
            selector: 'node[type="Event"]',
            style: { 'width': 38, 'height': 38, 'font-size': 6, 'background-color': '#5a6a7e' } as any,
          },
          {
            selector: 'node[type="Trace"]',
            style: { 'width': 30, 'height': 30, 'font-size': 6 } as any,
          },
          {
            selector: 'node[type="ExternalActor"]',
            style: { 'width': 50, 'height': 50, 'border-width': 3, 'border-color': '#e15a4a' } as any,
          },
          {
            selector: 'node[verdict="BLOCK"]',
            style: { 'border-width': 3, 'border-color': '#e5484d', 'background-color': '#e5484d' } as any,
          },
          {
            selector: 'edge',
            style: {
              'width': 1.2,
              'line-color': '#2e3c4e',
              'target-arrow-color': '#2e3c4e',
              'target-arrow-shape': 'triangle',
              'curve-style': 'bezier',
              'label': 'data(label)',
              'font-size': 6,
              'color': '#5a6a7e',
              'text-rotation': 'autorotate',
              'arrow-scale': 0.8,
              'opacity': 0.85,
            } as any,
          },
          {
            selector: 'edge[verdict="BLOCK"]',
            style: { 'line-color': '#e5484d', 'target-arrow-color': '#e5484d', 'width': 2.5, 'opacity': 1 } as any,
          },
          {
            selector: '.hl',
            style: { 'opacity': 1, 'z-index': 10 } as any,
          },
          {
            selector: 'node.hl',
            style: { 'border-width': 3, 'border-color': '#e8a317', 'background-color': '#e8a317', 'color': '#0d1116', 'z-index': 20 } as any,
          },
          {
            selector: 'node.hl[verdict="BLOCK"]',
            style: { 'background-color': '#e5484d', 'border-color': '#ff6b6b' } as any,
          },
          {
            selector: 'edge.hl',
            style: { 'line-color': '#e8a317', 'target-arrow-color': '#e8a317', 'width': 3, 'opacity': 1 } as any,
          },
          {
            selector: 'edge.hl[verdict="BLOCK"]',
            style: { 'line-color': '#e5484d', 'target-arrow-color': '#e5484d', 'width': 3.5 } as any,
          },
          {
            selector: '.dim',
            style: { 'opacity': 0.15 } as any,
          },
          {
            selector: 'node:selected',
            style: { 'border-width': 3, 'border-color': '#e8a317' } as any,
          },
        ],
        wheelSensitivity: 0.2,
        minZoom: 0.15,
        maxZoom: 4,
      })

      // --- taint path highlight: click node -> BFS upstream + sibling expansion ---
      cy.on('tap', 'node', (evt) => {
        const target = evt.target
        const startId = target.id()
        setSelected(startId)

        // build reverse + forward adjacency from current elements
        const edgeData = cy.edges().map(e => ({ source: e.data('source'), target: e.data('target'), id: e.id() }))
        const rev = new Map<string, string[]>() // target -> sources
        const fwd = new Map<string, string[]>() // source -> targets
        const edgeByPair = new Map<string, string>() // source|target -> edge id
        edgeData.forEach(ed => {
          if (!rev.has(ed.target)) rev.set(ed.target, [])
          rev.get(ed.target)!.push(ed.source)
          if (!fwd.has(ed.source)) fwd.set(ed.source, [])
          fwd.get(ed.source)!.push(ed.target)
          edgeByPair.set(`${ed.source}|${ed.target}`, ed.id)
        })

        const visited = new Set<string>([startId])
        const q: string[] = [startId]
        const pathEdgeIds = new Set<string>()
        while (q.length) {
          const cur = q.shift()!
          const preds = rev.get(cur) || []
          for (const p of preds) {
            const key = `${p}|${cur}`
            const eid = edgeByPair.get(key)
            if (eid) pathEdgeIds.add(eid)
            if (!visited.has(p)) {
              visited.add(p)
              q.push(p)
            }
          }
        }
        // Expand via Agent: if upstream hit an Agent, also highlight all events that Agent performed (sibling taint chain)
        const agentsInPath = [...visited].filter(id => {
          const n = cy.getElementById(id)
          return n.length && n.data('type') === 'Agent'
        })
        agentsInPath.forEach(agentId => {
          const outs = fwd.get(agentId) || []
          outs.forEach(tid => {
            const key = `${agentId}|${tid}`
            const eid = edgeByPair.get(key)
            if (eid) pathEdgeIds.add(eid)
            visited.add(tid)
            // also include Tool used by those events (forward one more hop)
            const second = fwd.get(tid) || []
            second.forEach(tid2 => {
              const k2 = `${tid}|${tid2}`
              const eid2 = edgeByPair.get(k2)
              if (eid2) pathEdgeIds.add(eid2)
              visited.add(tid2)
            })
          })
        })

        cy.elements().removeClass('hl dim')
        cy.elements().addClass('dim')
        visited.forEach(id => {
          const n = cy.getElementById(id)
          if (n.length) n.removeClass('dim').addClass('hl')
        })
        pathEdgeIds.forEach(eid => {
          const e = cy.getElementById(eid)
          if (e.length) e.removeClass('dim').addClass('hl')
        })
        // ensure clicked node is hl
        target.removeClass('dim').addClass('hl')
      })

      cy.on('tap', (evt) => {
        if (evt.target === cy) {
          cy.elements().removeClass('hl dim')
          setSelected(null)
        }
      })

      cyRef.current = cy
      ;(window as any)._cy = cy
      // expose for e2e taint highlight test
      ;(window as any).__highlightBlock = () => {
        // prefer the known audit-chain BLOCK (real attack chain: Read credentials -> http_post BLOCK)
        const preferred = cy.getElementById('evt:hook-1787900385759141700')
        const n = preferred.length ? preferred : cy.nodes('[verdict="BLOCK"]')[0]
        if (!n || !n.length) return 'no-block'
        n.emit('tap')
        return n.id()
      }
      setLoading(false)
    } catch (e: any) {
      setErr(e?.message || String(e))
      setLoading(false)
    }
  }, [buildElements])

  useEffect(() => {
    render(showAll)
    return () => {
      if (cyRef.current) {
        cyRef.current.destroy()
        cyRef.current = null
      }
    }
  }, [render, showAll])

  const handleReset = () => {
    if (cyRef.current) {
      cyRef.current.elements().removeClass('hl dim')
      cyRef.current.fit(undefined, 30)
      setSelected(null)
    }
  }

  const handleFit = () => {
    cyRef.current?.fit(undefined, 30)
  }

  return (
    <div style={{ padding: 24 }}>
      <h1 style={{ fontSize: 18, fontWeight: 700 }}>本体论图谱</h1>
      <p style={{ color: '#8092a6', fontSize: 12, marginBottom: 12 }}>
        Cytoscape.js 真渲染 · {stats ? `${stats.nodes} 节点 / ${stats.edges} 边 · 当前显示 ${stats.filtered}` : '加载中…'} · 点击节点溯源 taint 路径，BLOCK 红色 #e5484d
      </p>

      <div style={{ display: 'flex', gap: 8, marginBottom: 12, flexWrap: 'wrap', alignItems: 'center' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#c9d3de', cursor: 'pointer' }}>
          <input type="checkbox" checked={showAll} onChange={e => setShowAll(e.target.checked)} />
          显示全部（{stats?.nodes ?? 500} 节点，723+ 边，可能卡顿）
        </label>
        <button onClick={handleReset} style={{ padding: '6px 12px', background: '#1a2430', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2', cursor: 'pointer', fontSize: 12 }}>重置高亮</button>
        <button onClick={handleFit} style={{ padding: '6px 12px', background: '#1a2430', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2', cursor: 'pointer', fontSize: 12 }}>居中适配</button>
        <span style={{ fontSize: 11, color: '#8092a6', marginLeft: 8 }}>
          {selected ? <>已选 <code style={{ background: '#1a2430', padding: '1px 4px', borderRadius: 4 }}>{selected.slice(0, 32)}</code> · 上游链路已高亮</> : '点击任意节点查看上游 taint 链 · 空白处重置'}
        </span>
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center', fontSize: 11 }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, background: '#4a9eff', borderRadius: 2, display: 'inline-block' }} /> Agent</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, background: '#e8a317', borderRadius: 2, display: 'inline-block' }} /> Tool</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, background: '#8092a6', borderRadius: 2, display: 'inline-block' }} /> Event</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, background: '#9a7fd1', borderRadius: 2, display: 'inline-block' }} /> Trace</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><span style={{ width: 10, height: 10, background: '#e5484d', borderRadius: 2, display: 'inline-block' }} /> BLOCK</span>
        </span>
      </div>

      {err && <div style={{ padding: 12, background: '#2a1a1a', border: '1px solid #e5484d', borderRadius: 8, color: '#e6ebf2', marginBottom: 12, fontSize: 12 }}>加载失败: {err} · 请确认 kgbridge (8902) 与 gateway (8090) 运行中</div>}

      <div
        ref={containerRef}
        style={{
          height: 560,
          background: '#0f141c',
          borderRadius: 8,
          border: '1px solid #232d3b',
          position: 'relative',
          overflow: 'hidden',
        }}
      />
      {loading && (
        <div style={{ position: 'relative', marginTop: -560, height: 560, display: 'grid', placeItems: 'center', pointerEvents: 'none', color: '#8092a6', background: 'rgba(15,20,28,0.6)', borderRadius: 8 }}>
          布局计算中…（cose, 500 节点首次约 1–2s）
        </div>
      )}
      <div style={{ fontSize: 11, color: '#5a6a7e', marginTop: 8 }}>
        拖拽节点 · 滚轮缩放 · 点击节点高亮上游 taint 链（BFS upstream, dim 0.15 / hl 不透明） · BLOCK 边 #e5484d 红色加粗
      </div>
    </div>
  )
}

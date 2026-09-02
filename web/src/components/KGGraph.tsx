import { useEffect, useRef, useState, useCallback } from 'react'
import cytoscape from 'cytoscape'
import { api } from '../lib/api'
import { buildRiskSubgraph, type GraphMode, type KGNode, type KGEdge } from '../lib/graphModel'
import { Drawer } from './Drawer'
import { VerdictBadge } from './VerdictBadge'
import { Skeleton } from './Skeleton'

const TYPE_COLOR: Record<string, string> = {
  Agent: '#e8920c',   // 深橙（浅底高对比）
  Tool: '#1f7fd6',    // 深蓝
  Event: '#3d4d5c',   // 深灰蓝
  ExternalActor: '#c0392b', // 深红
}
const TYPE_SHAPE: Record<string, string> = {
  Agent: 'ellipse',
  Tool: 'round-rectangle',
  Event: 'round-rectangle',
  ExternalActor: 'hexagon',
}

function shortLabel(id: string, content: string, max = 20): string {
  const raw = content || id
  const cleaned = raw.replace(/^evt:/, '').replace(/^agent:@?/, '@').replace(/^tool:/, '')
  return cleaned.length > max ? cleaned.slice(0, max) + '…' : cleaned
}

const MODES: { id: GraphMode; label: string }[] = [
  { id: 'risk', label: '风险' },
  { id: 'review', label: '审查' },
  { id: 'full', label: '全量' },
]

export default function KGGraph({ focus }: { focus?: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<cytoscape.Core | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [mode, setMode] = useState<GraphMode>('risk')
  const [stats, setStats] = useState<{ shown: number; rawNodes: number; omitted: number; edges: number } | null>(null)
  const [selected, setSelected] = useState<KGNode | null>(null)
  const [tracing, setTracing] = useState(false)
  const rawRef = useRef<{ nodes: KGNode[]; edges: KGEdge[] } | null>(null)
  const nodeById = useRef<Map<string, KGNode>>(new Map())

  const render = useCallback(async (m: GraphMode) => {
    if (!containerRef.current) return
    const first = !rawRef.current
    if (first) setLoading(true)
    setErr(null)
    try {
      // 每次渲染都重拉数据（动态刷新），不缓存 rawRef
      const [nr, er] = await Promise.all([api.kgNodes(), api.kgEdges()])
      const n = Array.isArray((nr as any)?.nodes) ? (nr as any).nodes : Array.isArray(nr) ? nr : []
      const e = Array.isArray((er as any)?.edges) ? (er as any).edges : Array.isArray(er) ? er : []
      rawRef.current = { nodes: n, edges: e }
      nodeById.current = new Map(n.map((x: KGNode) => [x.id, x]))
      const { nodes, edges, stats } = buildRiskSubgraph(rawRef.current.nodes, rawRef.current.edges, m)
      setStats({ shown: stats.shown, rawNodes: stats.rawNodes, omitted: stats.omitted, edges: edges.length })

      if (cyRef.current) { cyRef.current.destroy(); cyRef.current = null }

      const cyNodes = nodes.map((n) => {
        const verdict = String(n.properties?.verdict || '')
        const risk = Number(n.properties?.risk || 0)
        const isBlock = verdict === 'BLOCK'
        const size = n.type === 'Event' ? (isBlock ? 30 + Math.min(risk / 4, 22) : verdict === 'CONFIRM' ? 34 : 26)
          : n.type === 'Agent' ? 52 : n.type === 'Tool' ? 46 : 40
        return {
          data: {
            id: n.id,
            label: shortLabel(n.id, n.content || ''),
            fullLabel: n.content || n.id,
            type: n.type,
            verdict,
            risk,
            isBlock,
            size,
            color: isBlock ? '#ff5f56' : verdict === 'CONFIRM' ? '#f5a623' : (TYPE_COLOR[n.type] || '#5f7183'),
            shape: TYPE_SHAPE[n.type] || 'ellipse',
          },
        }
      })
      const cyEdges = edges.map((e, i) => {
        const sv = String(nodeById.current.get(e.source)?.properties?.verdict || '')
        const tv = String(nodeById.current.get(e.target)?.properties?.verdict || '')
        const isBlock = sv === 'BLOCK' || tv === 'BLOCK'
        return { data: { id: `e${i}`, source: e.source, target: e.target, label: e.type || '', isBlock } }
      })

      const cy = cytoscape({
        container: containerRef.current,
        elements: [...cyNodes, ...cyEdges],
        layout: {
          name: 'cose', animate: true, animationDuration: 400,
          idealEdgeLength: 90, nodeOverlap: 14, refresh: 20, fit: true, padding: 36,
          randomize: false, componentSpacing: 60,
          nodeRepulsion: () => 480000, edgeElasticity: () => 120,
          nestingFactor: 1.2, gravity: 90, numIter: 900, initialTemp: 180, coolingFactor: 0.95, minTemp: 1.0,
        } as any,
        style: [
          { selector: 'node', style: {
            'background-color': 'data(color)', 'label': 'data(label)', 'color': '#1a2530',
            'font-size': 8, 'text-valign': 'bottom', 'text-halign': 'center', 'text-margin-y': 4,
            'text-wrap': 'wrap', 'text-max-width': 90, 'width': 'data(size)', 'height': 'data(size)',
            'border-width': 2, 'border-color': '#ffffff', 'shape': 'data(shape)', 'overlay-opacity': 0,
            'text-outline-width': 3, 'text-outline-color': '#ffffff',
          } as any },
          { selector: 'node[isBlock]', style: {
            'border-width': 3, 'border-color': '#ff5f56',
            'shadow-blur': 18, 'shadow-color': '#ff5f56', 'shadow-opacity': 0.5,
          } as any },
          { selector: 'edge', style: {
            'width': 1.2, 'line-color': '#2e3c4e', 'target-arrow-color': '#2e3c4e',
            'target-arrow-shape': 'triangle', 'curve-style': 'bezier', 'font-size': 6,
            'label': '', 'arrow-scale': 0.8, 'opacity': 0.8,
          } as any },
          { selector: 'edge[isBlock]', style: {
            'line-color': '#ff5f56', 'target-arrow-color': '#ff5f56', 'width': 2.4, 'opacity': 1,
            'line-style': 'dashed', 'line-dash-pattern': [6, 3],
          } as any },
          { selector: '.hl', style: { 'opacity': 1, 'z-index': 10 } as any },
          { selector: 'node.hl', style: { 'border-width': 3, 'border-color': '#f5a623', 'z-index': 20 } as any },
          { selector: 'edge.hl', style: { 'line-color': '#f5a623', 'target-arrow-color': '#f5a623', 'width': 3, 'opacity': 1 } as any },
          { selector: '.dim', style: { 'opacity': 0.13 } as any },
          { selector: 'node:selected', style: { 'border-width': 3, 'border-color': '#f5a623' } as any },
        ],
        wheelSensitivity: 0.2, minZoom: 0.12, maxZoom: 4,
      })

      // click node → BFS upstream highlight + drawer
      cy.on('tap', 'node', (evt) => {
        const target = evt.target
        const startId = target.id()
        setSelected(nodeById.current.get(startId) || null)

        const edgeData = cy.edges().map((e) => ({ source: e.data('source'), target: e.data('target'), id: e.id() }))
        const rev = new Map<string, string[]>()
        const fwd = new Map<string, string[]>()
        const edgeByPair = new Map<string, string>()
        edgeData.forEach((ed) => {
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
          for (const p of rev.get(cur) || []) {
            const eid = edgeByPair.get(`${p}|${cur}`)
            if (eid) pathEdgeIds.add(eid)
            if (!visited.has(p)) { visited.add(p); q.push(p) }
          }
        }
        // sibling expansion via Agent
        const agentsInPath = [...visited].filter((id) => cy.getElementById(id).data('type') === 'Agent')
        agentsInPath.forEach((agentId) => {
          for (const tid of fwd.get(agentId) || []) {
            const eid = edgeByPair.get(`${agentId}|${tid}`)
            if (eid) pathEdgeIds.add(eid)
            visited.add(tid)
            for (const tid2 of fwd.get(tid) || []) {
              const eid2 = edgeByPair.get(`${tid}|${tid2}`)
              if (eid2) pathEdgeIds.add(eid2)
              visited.add(tid2)
            }
          }
        })

        cy.elements().removeClass('hl dim')
        cy.elements().addClass('dim')
        visited.forEach((id) => { const n = cy.getElementById(id); if (n.length) n.removeClass('dim').addClass('hl') })
        pathEdgeIds.forEach((eid) => { const e = cy.getElementById(eid); if (e.length) e.removeClass('dim').addClass('hl') })
        target.removeClass('dim').addClass('hl')
      })

      cy.on('tap', (evt) => {
        if (evt.target === cy) { cy.elements().removeClass('hl dim'); setSelected(null) }
      })

      cyRef.current = cy
      ;(window as any)._cy = cy
      setLoading(false)

      // auto-focus from deep link (?focus=session_id): highlight first matching event
      if (focus) {
        const hit = cy.nodes().toArray().find((n) => n.id().includes(focus))
        if (hit) { hit.emit('tap'); cy.center(hit) }
      }
    } catch (e: any) {
      setErr(e?.message || String(e))
      setLoading(false)
    }
  }, [focus])

  useEffect(() => {
    render(mode)
    return () => { if (cyRef.current) { cyRef.current.destroy(); cyRef.current = null } }
  }, [render, mode])

  const handleReset = () => {
    cyRef.current?.elements().removeClass('hl dim')
    cyRef.current?.fit(undefined, 36)
    setSelected(null)
  }

  // Trace-to-source: pick the lowest-risk *source* node among highlighted
  // upstream and call /api/kg/graph/path to draw the full shortest lineage.
  const handleTrace = async () => {
    if (!selected || !cyRef.current) return
    setTracing(true)
    try {
      const cy = cyRef.current
      const hl = cy.nodes('.hl').toArray()
      // find a plausible source: Agent/Tool nodes in the highlighted upstream
      const sourceNode = hl.find((n) => n.data('type') === 'Agent') || hl[hl.length - 1]
      if (!sourceNode) return
      const res: any = await api.kgPath(sourceNode.id(), selected.id)
      const path: string[] = res?.path || res?.nodes?.map((n: any) => n.id) || []
      if (path.length) {
        cy.elements().removeClass('hl dim')
        cy.elements().addClass('dim')
        path.forEach((id) => { const n = cy.getElementById(id); if (n.length) n.removeClass('dim').addClass('hl') })
        // highlight edges along the path
        for (let i = 0; i < path.length - 1; i++) {
          const a = path[i]; const b = path[i + 1]
          cy.edges().toArray().forEach((e) => {
            const s = e.data('source'); const t = e.data('target')
            if ((s === a && t === b) || (s === b && t === a)) e.removeClass('dim').addClass('hl')
          })
        }
      }
    } catch { /* path may not exist — keep BFS highlight */ }
    setTracing(false)
  }

  // 自动刷新：每 30s 重拉图谱数据并重排（动态高帧）
  useEffect(() => {
    render(mode)
    const t = setInterval(() => render(mode), 30000)
    return () => clearInterval(t)
  }, [mode, render])

  return (
    <div style={{ padding: '16px 22px', display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <div className="row-between" style={{ marginBottom: 10, flexWrap: 'wrap', gap: 10 }}>
        <div className="row" style={{ gap: 12 }}>
          <div className="seg">
            {MODES.map((x) => (
              <button key={x.id} className={`seg-item ${mode === x.id ? 'on' : ''}`} onClick={() => setMode(x.id)}>{x.label}</button>
            ))}
          </div>
          <span className="small dim">
            {stats ? `显示 ${stats.shown} / ${stats.rawNodes} 节点 · ${stats.edges} 边${stats.omitted > 0 ? ` · 已省略 ${stats.omitted} 个低风险` : ''}` : '加载中…'}
          </span>
        </div>
        <div className="row" style={{ gap: 8 }}>
          <button className="btn" onClick={handleReset}>重置高亮</button>
          <button className="btn" onClick={() => cyRef.current?.fit(undefined, 36)}>居中适配</button>
          <span className="row small dim" style={{ gap: 10 }}>
            <Legend c="#e8920c" t="Agent" /><Legend c="#1f7fd6" t="Tool" />
            <Legend c="#3d4d5c" t="Event" /><Legend c="#c0392b" t="BLOCK" />
          </span>
        </div>
      </div>

      {mode === 'full' && (
        <div className="card card-pad small" style={{ marginBottom: 10, borderColor: 'rgba(245,166,35,.35)', background: 'rgba(245,166,35,.06)', color: 'var(--confirm)' }}>
          全量档节点较多，可能卡顿；「风险」档只展示 BLOCK 及其上游链，是日常追溯的推荐视图。
        </div>
      )}
      {err && (
        <div className="card card-pad small" style={{ marginBottom: 10, borderColor: 'rgba(255,95,86,.4)', background: 'rgba(255,95,86,.08)', color: 'var(--block)' }}>
          加载失败: {err} · 请确认 kgbridge (8902) 与 gateway (8090) 运行中
        </div>
      )}

      <div ref={containerRef} style={{ flex: 1, minHeight: 420, background: 'var(--bg-1)', borderRadius: 'var(--r-m)', border: '1px solid var(--line)', overflow: 'hidden' }} />
      {loading && <Skeleton h={420} style={{ marginTop: -420, borderRadius: 'var(--r-m)' }} />}

      <div className="small dim" style={{ marginTop: 8 }}>
        拖拽 · 滚轮缩放 · 点击节点高亮上游 taint 链 · BLOCK 节点带红色外发光，taint 边为红色流动虚线
      </div>

      <Drawer open={!!selected} onClose={() => setSelected(null)}
        title={selected ? <span className="row" style={{ gap: 8 }}><VerdictBadge v={selected.properties?.verdict} /> {shortLabel(selected.id, selected.content || '', 30)}</span> : ''}>
        {selected && (
          <div className="col" style={{ gap: 14 }}>
            <dl className="kv">
              <dt>ID</dt><dd className="mono">{selected.id}</dd>
              <dt>类型</dt><dd>{selected.type}</dd>
              <dt>裁决</dt><dd><VerdictBadge v={selected.properties?.verdict} /></dd>
              <dt>风险分</dt><dd>{selected.properties?.risk ?? 0}</dd>
            </dl>
            {selected.properties?.rationale && (
              <div className="card card-pad">
                <div className="h-sec" style={{ marginBottom: 6 }}>判定理由</div>
                <div className="small" style={{ wordBreak: 'break-all' }}>{selected.properties.rationale}</div>
              </div>
            )}
            <button className="btn btn-primary" disabled={tracing} onClick={handleTrace}>
              {tracing ? '追溯中…' : '追溯到源头 →'}
            </button>
            <div className="small dim">调用 /api/kg/graph/path 画出从敏感源到该事件的完整最短血缘路径。</div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

function Legend({ c, t }: { c: string; t: string }) {
  return <span className="row" style={{ gap: 4 }}><span style={{ width: 9, height: 9, background: c, borderRadius: 2, display: 'inline-block' }} />{t}</span>
}

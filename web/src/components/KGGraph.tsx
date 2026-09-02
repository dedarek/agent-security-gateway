import { useEffect, useRef, useState, useCallback } from 'react'
import cytoscape from 'cytoscape'
import fcose from 'cytoscape-fcose'
import { api } from '../lib/api'
import { buildOntoSubgraph, type GraphMode, type KGNode, type KGEdge } from '../lib/graphModel'
import { Drawer } from './Drawer'
import { VerdictBadge } from './VerdictBadge'
import { Skeleton } from './Skeleton'

// 注册 fcose 力导向布局（比内置 cose 更均匀、无重叠、更好看）
let _fcoseReg = false
if (!_fcoseReg) { try { cytoscape.use(fcose as any); _fcoseReg = true } catch { /* already registered */ } }

const TYPE_COLOR: Record<string, string> = {
  Agent: '#ffb020',   // 暖琥珀（深底霓虹）
  Tool: '#3ba7ff',    // 亮蓝
  Event: '#7d8ca3',   // 冷灰蓝
  ExternalActor: '#ff4d5e', // 亮红
}
const TYPE_SHAPE: Record<string, string> = {
  Agent: 'ellipse',
  Tool: 'round-rectangle',
  Event: 'round-rectangle',
  ExternalActor: 'hexagon',
}

function shortLabel(id: string, content: string, max = 28): string {
  const raw = content || id
  const cleaned = raw.replace(/^evt:/, '').replace(/^agent:@?/, '@').replace(/^tool:/, '').replace(/^org:file:/, '')
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
  const rawRef = useRef<{ nodes: KGNode[]; edges: KGEdge[] } | null>(null)
  const nodeById = useRef<Map<string, KGNode>>(new Map())
  const animRef = useRef<number | null>(null)

  const render = useCallback(async (m: GraphMode) => {
    if (!containerRef.current) return
    const first = !rawRef.current
    if (first) setLoading(true)
    setErr(null)
    try {
      // 拉取「统一本体」数据（agent/origin/story/verdict），而非底层 probe 图
      const og: any = await api.ontoGraph()
      const rawN = Array.isArray(og?.nodes) ? og.nodes : []
      const rawEdg = Array.isArray(og?.edges) ? og.edges : []
      rawRef.current = { nodes: rawN, edges: rawEdg }
      const { nodes, edges, stats } = buildOntoSubgraph({ nodes: rawN, edges: rawEdg }, m)
      nodeById.current = new Map(nodes.map((x: KGNode) => [x.id, x]))
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
          name: 'fcose', animate: true, animationDuration: 900, animationEasing: 'ease-out',
          quality: 'proof', randomize: true, fit: true, padding: 44,
          nodeSeparation: 130, idealEdgeLength: () => 120, edgeElasticity: () => 0.35,
          nodeRepulsion: () => 12000, gravity: 0.28, gravityRange: 3.2,
          numIter: 2500, tile: true, uniformNodeDimensions: false,
        } as any,
        style: [
          { selector: 'node', style: {
            'background-color': 'data(color)', 'label': 'data(label)', 'color': '#eaf1fb',
            'font-size': 11, 'font-weight': 700, 'text-valign': 'bottom', 'text-halign': 'center', 'text-margin-y': 6,
            'text-wrap': 'wrap', 'text-max-width': 140, 'width': 'data(size)', 'height': 'data(size)',
            'border-width': 2, 'border-color': 'rgba(255,255,255,.35)', 'shape': 'data(shape)', 'overlay-opacity': 0,
            'text-outline-width': 3, 'text-outline-color': '#060b18',
            'background-opacity': 0.96,
          } as any },
          { selector: 'node[isBlock]', style: {
            'border-width': 3, 'border-color': '#ff5f56',
            'shadow-blur': 26, 'shadow-color': '#ff5f56', 'shadow-opacity': 0.75,
          } as any },
          { selector: 'edge', style: {
            'width': 1.4, 'line-color': '#3a4d6b', 'target-arrow-color': '#3a4d6b',
            'target-arrow-shape': 'triangle', 'curve-style': 'bezier', 'font-size': 6,
            'label': '', 'arrow-scale': 0.8, 'opacity': 0.7,
          } as any },
          { selector: 'edge[isBlock]', style: {
            'line-color': '#ff5f56', 'target-arrow-color': '#ff5f56', 'width': 2.6, 'opacity': 1,
            'line-style': 'dashed', 'line-dash-pattern': [7, 4], 'line-dash-offset': 0,
          } as any },
          { selector: '.hl', style: { 'opacity': 1, 'z-index': 10 } as any },
          { selector: 'node.hl', style: { 'border-width': 3, 'border-color': '#ffd54a', 'shadow-blur': 22, 'shadow-color': '#ffd54a', 'shadow-opacity': 0.8, 'z-index': 20 } as any },
          { selector: 'edge.hl', style: { 'line-color': '#ffd54a', 'target-arrow-color': '#ffd54a', 'width': 3.2, 'opacity': 1 } as any },
          { selector: '.dim', style: { 'opacity': 0.1 } as any },
          { selector: 'node:selected', style: { 'border-width': 3, 'border-color': '#ffd54a' } as any },
          // hover 涟漪效果
          { selector: 'node.hover', style: {
            'width': (n: any) => n.data('size') * 1.28, 'height': (n: any) => n.data('size') * 1.28,
            'border-width': 4, 'border-color': '#ffffff',
            'shadow-blur': 30, 'shadow-color': (n: any) => n.data('color'), 'shadow-opacity': 0.95,
            'transition-property': 'width height border-width shadow-blur', 'transition-duration': '160ms', 'z-index': 30,
          } as any },
          { selector: 'node.hoverNbr', style: { 'border-width': 3, 'border-color': 'rgba(255,255,255,.7)', 'z-index': 15 } as any },
          { selector: 'edge.hoverEdge', style: {
            'line-color': '#8fd0ff', 'target-arrow-color': '#8fd0ff', 'width': 3, 'opacity': 1, 'z-index': 15,
          } as any },
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

      // hover 涟漪：悬停节点放大+高亮直连邻居，移出复原
      cy.on('mouseover', 'node', (evt) => {
        const n = evt.target
        n.addClass('hover')
        n.connectedEdges().addClass('hoverEdge')
        n.neighborhood('node').addClass('hoverNbr')
        if (containerRef.current) containerRef.current.style.cursor = 'pointer'
      })
      cy.on('mouseout', 'node', (evt) => {
        const n = evt.target
        n.removeClass('hover')
        cy.elements().removeClass('hoverEdge hoverNbr')
        if (containerRef.current) containerRef.current.style.cursor = 'default'
      })

      cyRef.current = cy
      ;(window as any)._cy = cy
      setLoading(false)

      // 连续动效：taint 边流动（marching ants）+ BLOCK 节点呼吸脉冲，高帧
      if (animRef.current) cancelAnimationFrame(animRef.current)
      let dash = 0
      let t0 = performance.now()
      const blockNodes = cy.nodes().filter((n: any) => n.data('isBlock'))
      const tick = (now: number) => {
        dash = (dash - 0.6)
        cy.edges('[isBlock]').style('line-dash-offset', dash)
        const phase = (Math.sin((now - t0) / 620) + 1) / 2 // 0..1
        blockNodes.style('shadow-blur', 18 + phase * 20)
        blockNodes.style('shadow-opacity', 0.5 + phase * 0.4)
        animRef.current = requestAnimationFrame(tick)
      }
      animRef.current = requestAnimationFrame(tick)

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

  // 自动刷新：每 30s 重拉图谱数据并重排（动态高帧）
  useEffect(() => {
    render(mode)
    const t = setInterval(() => render(mode), 30000)
    return () => { clearInterval(t); if (animRef.current) cancelAnimationFrame(animRef.current) }
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
            <Legend c="#ffb020" t="智能体" /><Legend c="#ff4d5e" t="拦截" />
            <Legend c="#3ba7ff" t="敏感资源" /><Legend c="#7d8ca3" t="会话" />
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

      <div ref={containerRef} style={{ flex: 1, minHeight: 420, background: 'radial-gradient(120% 120% at 30% 20%, #0f1b33 0%, #0a1224 55%, #060b18 100%)', borderRadius: 'var(--r-m)', border: '1px solid #1c2b47', overflow: 'hidden', boxShadow: 'inset 0 0 80px rgba(0,0,0,.45)' }} />
      {loading && <Skeleton h={420} style={{ marginTop: -420, borderRadius: 'var(--r-m)' }} />}

      <div className="small dim" style={{ marginTop: 8 }}>
        智能体 ▸ 触发拦截 ▸ 涉及敏感资源 · 点击任意节点高亮它的完整安全链路 · 红色发光节点=高危拦截，红色流动虚线=污点传播
      </div>

      <Drawer open={!!selected} onClose={() => setSelected(null)}
        title={selected ? <span className="row" style={{ gap: 8 }}><VerdictBadge v={selected.properties?.verdict} /> {shortLabel(selected.id, selected.content || '', 30)}</span> : ''}>
        {selected && (
          <div className="col" style={{ gap: 14 }}>
            <dl className="kv">
              <dt>类型</dt><dd>{typeCN(selected.type, selected.properties)}</dd>
              {selected.properties?.verdict === 'BLOCK' && <><dt>裁决</dt><dd><VerdictBadge v="BLOCK" /></dd></>}
              {selected.properties?.risk != null && Number(selected.properties?.risk) > 0 && <><dt>风险分</dt><dd>{selected.properties?.risk}</dd></>}
              {selected.properties?.tool && <><dt>工具</dt><dd className="mono">{selected.properties.tool}</dd></>}
              {selected.properties?.agent && <><dt>智能体</dt><dd>{selected.properties.agent}</dd></>}
              {selected.properties?.session && <><dt>会话</dt><dd className="mono">{selected.properties.session}</dd></>}
              {selected.properties?.steps != null && <><dt>步数</dt><dd>{selected.properties.steps}</dd></>}
              {selected.properties?.at && <><dt>时间</dt><dd>{selected.properties.at}</dd></>}
              {selected.properties?.kind === 'resource' && <><dt>敏感</dt><dd>{selected.properties?.sensitive ? '是 · 凭证/密钥' : '否'}</dd></>}
              {selected.properties?.reads != null && <><dt>被读取</dt><dd>{selected.properties.reads} 次</dd></>}
            </dl>
            <div className="small dim">已在图中高亮该节点的完整安全链路（上游智能体 · 拦截事件 · 涉及资源）。点击空白处取消高亮。</div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

function typeCN(type: string, props?: Record<string, any>): string {
  if (props?.kind === 'resource') return '敏感资源'
  if (type === 'Agent') return '智能体'
  if (type === 'Event') return props?.verdict === 'BLOCK' ? '高危拦截' : '会话'
  return type
}

function Legend({ c, t }: { c: string; t: string }) {
  return <span className="row" style={{ gap: 4 }}><span style={{ width: 9, height: 9, background: c, borderRadius: 2, display: 'inline-block' }} />{t}</span>
}

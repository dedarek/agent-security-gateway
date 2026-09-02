import { useEffect, useRef, useState } from 'react'
import { Graph as G6Graph } from '@antv/g6'
import { api } from '../lib/api'

// ── AntV G6 5.x 本体图谱 — 三层血缘下钻 ─────────────────────────────────────
// B 层(污点血缘): origin(敏感源) --flows_to--> sink(外发)
// D 层(证据链):   story --resulted_in--> verdict (BLOCK/CONFIRM)
// E 层(会话叙事): story --narrates--> agent
// 视觉约定 (单一口径, 不许两处图例打架):
//   Agent=蓝圆  Story=灰圆  Verdict=橙三角  Origin=绿菱形

const C = {
  bg: '#0b1220',
  agent: '#3b82f6', story: '#64748b', verdict: '#f97316', origin: '#10b981',
  text: '#e6edf7', dim: '#8fa3bf',
  edgeNarrate: 'rgba(100,116,139,.45)', edgeResult: 'rgba(249,115,22,.6)', edgeRead: 'rgba(16,185,129,.55)',
}

const SHAPES: Record<string, string> = { agent: 'circle', story: 'circle', verdict: 'triangle', origin: 'diamond' }
// 节点大小按重要性分级: Agent 最大(发起者) > 敏感资源(核心资产) > BLOCK裁决 > 会话(上下文)
const SIZES: Record<string, number> = { agent: 56, origin: 48, verdict: 44, story: 28 }

// G6 5.x 的 labels 是 "data.label"，layout 结果通过 graph.getNodeData(id) 读
// 用 dagre 做分层: rankdir LR => 左(Agent/Origin)→中(Story)→右(Verdict/Sink)
// 手动分层: 按类型分配 X 列坐标, 同类型内 Y 均分 (备用: 若 force 布局效果不好可切回)
// 当前用 force 布局自然散开, 手动坐标仅作初始位置参考
function buildLayoutData(raw: any) {
  // 默认只显示有高危/被拦截的会话 + 其关联节点, 减少 36 个灰色会话的杂乱
  // 全部模式可通过 UI 切换
  const allNodes = raw.nodes.map((n: any) => ({
    id: n.id,
    data: { ...n, color: C[n.type as keyof typeof C] || C.story, shape: SHAPES[n.type] || 'circle', size: SIZES[n.type] || 24 },
  }))
  const allEdges = raw.edges.map((e: any, i: number) => ({ id: `e${i}`, source: e.source, target: e.target, data: e }))

  // 高危会话 = outcome=blocked 或 peak_risk >= 60
  const highRiskStoryIds = new Set(
    allNodes
      .filter((n: any) => n.data.type === 'story' && (n.data.props?.outcome === 'blocked' || (n.data.props?.peak_risk || 0) >= 60))
      .map((n: any) => n.id)
  )

  // 收集相关节点: 高危会话 + 其连接的 agent/verdict/origin/sink
  const related = new Set<string>()
  allEdges.forEach((e: any) => {
    if (highRiskStoryIds.has(e.source) || highRiskStoryIds.has(e.target)) {
      related.add(e.source); related.add(e.target)
    }
  })
  // 也要包含 verdict 的父会话 (resulted_in 边)
  allEdges.forEach((e: any) => {
    if (e.data.type === 'resulted_in' && related.has(e.target)) {
      related.add(e.source)
    }
  })
  // agent 总是保留 (它们是枢纽)
  allNodes.forEach((n: any) => { if (n.data.type === 'agent') related.add(n.id) })

  const nodes = allNodes.filter((n: any) => related.has(n.id))
  const edges = allEdges.filter((e: any) => related.has(e.source) && related.has(e.target))

  return { nodes, edges, totalNodes: allNodes.length, shownNodes: nodes.length }
}

export default function OntologyGraph({ focus }: { focus?: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const graphRef = useRef<any>(null)
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [stats, setStats] = useState<{ agent: number; story: number; verdict: number; origin: number } | null>(null)
  const [selected, setSelected] = useState<{ id: string; type: string; label: string; props?: any } | null>(null)
  const [evidence, setEvidence] = useState<any>(null)
  const [evLoading, setEvLoading] = useState(false)

  useEffect(() => {
    if (!containerRef.current) return
    let disposed = false
    const run = async () => {
      try {
        const og: any = await api.ontoGraph()
        // API 返回 { nodes: [...], edges: [...] } 或直接 { nodes, edges }
        const raw = og?.nodes ? og : { nodes: [], edges: [] }
        if (!raw.nodes || raw.nodes.length === 0) {
          setErr('图谱数据为空 — 请确认 agent 有高危事件')
          setLoading(false)
          return
        }
        const { nodes, edges, totalNodes, shownNodes } = buildLayoutData(raw)

        setStats({
          agent: nodes.filter((n: any) => n.data.type === 'agent').length,
          story: nodes.filter((n: any) => n.data.type === 'story').length,
          verdict: nodes.filter((n: any) => n.data.type === 'verdict').length,
          origin: nodes.filter((n: any) => n.data.type === 'origin').length,
        })
        // 显示过滤比例
        if (shownNodes < totalNodes) {
          console.log(`[KG] 过滤: 显示 ${shownNodes}/${totalNodes} 节点 (高危会话优先)`)
        }

        if (graphRef.current) { graphRef.current.destroy(); graphRef.current = null }

        const graph = new G6Graph({
          container: containerRef.current!,
          width: containerRef.current!.clientWidth,
          height: Math.max(containerRef.current!.clientHeight, 460),
          animation: true,
          autoFit: 'view',
          padding: 36,
          // 用 force 布局让节点自然散开 (dagre/preset 在 G6 5.x 不稳定)
          // 通过 gravity + linkDistance 控制分层感
          layout: {
            type: 'force',
            preventOverlap: true,
            nodeSpacing: 60,
            linkDistance: 160,
            nodeStrength: -200,
            edgeStrength: 0.6,
            gravity: 0.1,
            alpha: 0.5,
            alphaDecay: 0.05,
            alphaMin: 0.05,
          },
          node: {
            style: {
              size: (d: any) => d.data.size,
              fill: (d: any) => d.data.color,
              // BLOCK 节点不显示文字 (颜色+形状已足够), 会话截断 UUID
              labelText: (d: any) => {
                if (d.data.type === 'verdict') return '' // 不显示 "BLOCK" 文字
                const lbl = d.data.label || d.id
                return lbl.length > 16 ? lbl.slice(0, 16) + '…' : lbl
              },
              labelFill: C.text,
              labelFontSize: 10,
              labelPlacement: 'bottom',
              labelOffsetY: 4,
              labelMaxWidth: 90,
              stroke: (d: any) => d.data.type === 'verdict' ? '#ffb020' : 'rgba(255,255,255,.22)',
              lineWidth: (d: any) => d.data.type === 'verdict' ? 2.2 : 1.2,
              shadowColor: (d: any) => d.data.type === 'verdict' ? '#f97316' : 'transparent',
              shadowBlur: (d: any) => d.data.type === 'verdict' ? 18 : 0,
            },
            state: {
              active: { stroke: '#ffd54a', lineWidth: 3, shadowColor: '#ffd54a', shadowBlur: 24 },
              dim: { fillOpacity: .12, strokeOpacity: .1, labelFillOpacity: .2 },
            },
          },
          edge: {
            style: {
              stroke: (d: any) => {
                const t = d.data?.type
                return t === 'resulted_in' ? 'rgba(249,115,22,.75)' :  // 橙: 会话→裁决
                       t === 'reads' ? 'rgba(16,185,129,.65)' :          // 绿: 智能体→资源
                       t === 'narrates' ? 'rgba(100,116,139,.5)' :        // 灰: 会话→智能体
                       'rgba(100,116,139,.4)'
              },
              lineWidth: (d: any) => d.data?.type === 'resulted_in' ? 2 : 1.3,
              endArrow: true,
              endArrowSize: 8,
              endArrowFill: (d: any) => d.data?.type === 'resulted_in' ? '#f97316' : d.data?.type === 'reads' ? '#10b981' : '#64748b',
              strokeOpacity: .9,
            },
          },
          behaviors: ['drag-canvas', 'zoom-canvas', 'drag-element'],
        })

        graphRef.current = graph

        // 点击节点: 高亮 2-hop 邻居 + 若为 verdict 则拉证据
        graph.on('node:click', async (evt: any) => {
          const id = evt.target?.id || evt.itemId
          if (!id) return
          const data = nodes.find((n: any) => n.id === id)?.data || {}
          setSelected({ id, type: data.type || '', label: data.label || id, props: data.props })

          // 高亮 2-hop 邻居
          const all = graph.getData()
          const adj: Record<string, string[]> = {}
          all.edges.forEach((e: any) => { (adj[e.source] ||= []).push(e.target); (adj[e.target] ||= []).push(e.source) })
          const seen = new Set([id]); let frontier = [id]
          for (let hop = 0; hop < 2; hop++) {
            const next: string[] = []
            frontier.forEach((f) => (adj[f] || []).forEach((nb) => { if (!seen.has(nb)) { seen.add(nb); next.push(nb) } }))
            frontier = next
          }
          graph.setElementState({}); // clear
          const toActive = [...seen]
          const toDim = all.nodes.map((n: any) => n.id).filter((x: string) => !seen.has(x))
          graph.setElementState(toActive, 'active')
          graph.setElementState(toDim, 'dim')

          // verdict -> 证据链 (D 层)
          if (data.type === 'verdict' && data.props?.session) {
            setEvLoading(true); setEvidence(null)
            try {
              // evidence API: event = CallID, verdict node id 通常是 "verdict:<callid>"
              const callId = id.replace(/^verdict:/, '')
              const ev: any = await api.ontoEvidence(callId)
              setEvidence(ev)
            } catch (e: any) {
              setEvidence({ error: e.message })
            }
            setEvLoading(false)
          } else {
            setEvidence(null)
          }
        })

        graph.on('canvas:click', () => {
          graph.setElementState({}); setSelected(null); setEvidence(null)
        })

        await graph.setData({ nodes, edges })
        await graph.render()
        // 等布局跑完再 fit，否则 dagre 的坐标还没算出来
        setTimeout(() => {
          try { graph.fitView(40); } catch (e) { /* ignore */ }
        }, 200)
        setLoading(false)

        if (focus) {
          const hit = nodes.find((n: any) => n.id.includes(focus))
          if (hit) graph.emit('node:click', { target: { id: hit.id }, itemId: hit.id })
        }
      } catch (e: any) {
        setErr(e?.message || String(e))
        setLoading(false)
      }
    }
    run()
    return () => { disposed = true; if (graphRef.current) { graphRef.current.destroy(); graphRef.current = null } }
  }, [focus])

  const onResize = () => {
    if (graphRef.current && containerRef.current) {
      graphRef.current.resize(containerRef.current.clientWidth, Math.max(containerRef.current.clientHeight, 460))
      graphRef.current.fitView()
    }
  }
  useEffect(() => { window.addEventListener('resize', onResize); return () => window.removeEventListener('resize', onResize) }, [])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, padding: '8px 4px 12px', fontSize: 12.5, color: C.dim }}>
        <span style={{ color: C.agent }}>●</span> Agent <b style={{ color: C.text }}>{stats?.agent ?? 0}</b>
        <span style={{ color: C.story }}>●</span> 会话 <b style={{ color: C.text }}>{stats?.story ?? 0}</b>
        <span style={{ color: C.verdict }}>▲</span> 裁决 <b style={{ color: C.text }}>{stats?.verdict ?? 0}</b>
        <span style={{ color: C.origin }}>◆</span> 敏感资源 <b style={{ color: C.text }}>{stats?.origin ?? 0}</b>
        <span style={{ marginLeft: 'auto', fontSize: 12 }}>🖱 滚轮缩放 · 拖拽平移 · 点节点下钻 · 点空白复位</span>
      </div>

      <div style={{ flex: 1, position: 'relative', borderRadius: 10, overflow: 'hidden', border: '1px solid #1c2b47', background: C.bg, minHeight: 460 }}>
        <div ref={containerRef} style={{ position: 'absolute', inset: 0 }} />
        {loading && <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', color: C.dim }}>加载图谱…</div>}
        {err && <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', color: '#f87171' }}>加载失败: {err}</div>}

        {/* 选中节点的详情抽屉 */}
        {selected && (
          <div style={{
            position: 'absolute', top: 12, right: 12, bottom: 12, width: 300, zIndex: 20,
            background: 'rgba(13,22,38,.96)', border: '1px solid #22334f', borderRadius: 12, padding: 14,
            overflow: 'auto', backdropFilter: 'blur(6px)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
              <div style={{ fontSize: 14, fontWeight: 800, color: C.text }}>{selected.label}</div>
              <button className="btn" style={{ fontSize: 11, padding: '3px 8px' }} onClick={() => { setSelected(null); setEvidence(null); graphRef.current?.setElementState?.({}) }}>复位</button>
            </div>
            <div style={{ fontSize: 12.5, color: C.dim, lineHeight: 2 }}>
              <div>类型: <b style={{ color: C.text }}>{selected.type}</b></div>
              {selected.props?.risk != null && selected.props.risk > 0 && <div>风险分: <b style={{ color: C.verdict }}>{selected.props.risk}</b></div>}
              {selected.props?.steps != null && <div>步数: {selected.props.steps}</div>}
              {selected.props?.outcome && <div>结果: <b style={{ color: selected.props.outcome === 'blocked' ? C.verdict : C.origin }}>{selected.props.outcome === 'blocked' ? '被拦截' : selected.props.outcome}</b></div>}
              {selected.props?.reads != null && <div>被读取: {selected.props.reads} 次</div>}
              {selected.props?.agent && <div>智能体: {selected.props.agent}</div>}
              {selected.props?.at && <div>时间: {selected.props.at}</div>}
            </div>

            {selected.type === 'verdict' && (
              <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid #22334f' }}>
                <div style={{ fontSize: 12.5, fontWeight: 700, color: C.text, marginBottom: 8 }}>证据链 (D 层)</div>
                {evLoading && <div style={{ color: C.dim, fontSize: 12 }}>拉取中…</div>}
                {evidence?.error && <div style={{ color: '#f87171', fontSize: 12 }}>{evidence.error}</div>}
                {evidence && !evidence.error && (
                  <div style={{ fontSize: 12.5, lineHeight: 1.9 }}>
                    <div style={{ color: C.dim }}>最终裁决: <b style={{ color: evidence.final === 'BLOCK' ? C.verdict : C.origin }}>{evidence.final}</b> · 风险 {evidence.risk}</div>
                    {evidence.rationale && <div style={{ color: C.dim, fontSize: 11.5, wordBreak: 'break-all' }}>{evidence.rationale.slice(0, 120)}{evidence.rationale.length > 120 ? '…' : ''}</div>}
                    {evidence.sole_axis && <div style={{ color: C.origin, fontSize: 11.5 }}>⚡ 单引擎驱动</div>}
                    {evidence.taint_from && <div style={{ color: C.dim, fontSize: 11.5 }}>污点源: {evidence.taint_from}</div>}
                    {evidence.votes && evidence.votes.length > 0 && (
                      <div style={{ marginTop: 8 }}>
                        <div style={{ fontSize: 11.5, fontWeight: 600, color: C.dim, marginBottom: 4 }}>引擎投票:</div>
                        {evidence.votes.map((v: any, i: number) => (
                          <div key={i} style={{ display: 'flex', justifyContent: 'space-between', padding: '3px 0', borderBottom: '1px solid rgba(255,255,255,.05)' }}>
                            <span style={{ color: C.text }}>{v.engine || 'engine'}</span>
                            <span>
                              <span style={{ color: v.vote === 'BLOCK' ? C.verdict : v.vote === 'CONFIRM' ? '#f59e0b' : C.origin }}>{v.vote}</span>
                              <span style={{ color: C.dim, marginLeft: 6 }}>{v.score}分</span>
                            </span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      <div style={{ marginTop: 8, fontSize: 12, color: C.dim }}>
        <b style={{ color: C.agent }}>Agent(蓝)</b> 发起操作 → <b style={{ color: C.story }}>会话(灰)</b> 累积上下文 → <b style={{ color: C.verdict }}>BLOCK(橙)</b> 裁决 → <b style={{ color: C.origin }}>敏感资源(绿)</b> 被触碰
      </div>
    </div>
  )
}

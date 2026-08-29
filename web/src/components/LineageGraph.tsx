import { useEffect, useRef, useState } from 'react'
import cytoscape from 'cytoscape'
import { api } from '../lib/api'
import { ontoToCy } from '../lib/ontoModel'
import { Skeleton } from './Skeleton'

/** B layer — focused taint-lineage subgraph around a node. */
export function LineageGraph({ focus, onPickNode, onPickEdge }: {
  focus?: string
  onPickNode?: (id: string, props: any) => void
  onPickEdge?: (props: any) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const cyRef = useRef<cytoscape.Core | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState<string | null>(null)
  const [meta, setMeta] = useState<{ nodes: number; edges: number } | null>(null)

  useEffect(() => {
    let dead = false
    setLoading(true)
    setErr(null)
    ;(async () => {
      try {
        const data = focus ? await api.ontoLineage(focus) : await api.ontoGraph()
        if (dead) return
        const elements = ontoToCy(data.nodes || [], data.edges || [])
        setMeta({ nodes: (data.nodes || []).length, edges: (data.edges || []).length })
        if (!ref.current) return
        if (cyRef.current) { cyRef.current.destroy(); cyRef.current = null }
        const cy = cytoscape({
          container: ref.current,
          elements,
          layout: { name: 'cose', animate: true, animationDuration: 400, idealEdgeLength: 100, nodeOverlap: 16, fit: true, padding: 40, nodeRepulsion: () => 500000, numIter: 900 } as any,
          style: [
            { selector: 'node', style: {
              'background-color': 'data(color)', 'label': 'data(label)', 'color': 'var(--fg-0)',
              'font-size': 8, 'text-valign': 'bottom', 'text-halign': 'center', 'text-margin-y': 4,
              'text-wrap': 'wrap', 'text-max-width': 90, 'width': 'data(size)', 'height': 'data(size)',
              'border-width': 2, 'border-color': '#fff', 'shape': 'data(shape)', 'overlay-opacity': 0,
            } as any },
            { selector: 'node[sensitive]', style: { 'border-width': 3, 'border-color': 'var(--confirm)' } as any },
            { selector: 'node[verdict="BLOCK"]', style: { 'border-color': 'var(--block)', 'shadow-blur': 16, 'shadow-color': '#d93025', 'shadow-opacity': 0.4 } as any },
            { selector: 'edge', style: {
              'width': 1.4, 'line-color': 'var(--line-2)', 'target-arrow-color': 'var(--line-2)',
              'target-arrow-shape': 'triangle', 'curve-style': 'bezier', 'label': 'data(label)',
              'font-size': 7, 'color': 'var(--fg-2)', 'text-rotation': 'autorotate', 'arrow-scale': 0.9, 'opacity': 0.85,
            } as any },
            { selector: 'edge[tainted]', style: {
              'line-color': 'var(--block)', 'target-arrow-color': 'var(--block)', 'width': 2.4,
              'line-style': 'dashed', 'line-dash-pattern': [6, 3], 'opacity': 1,
            } as any },
            { selector: 'edge[isBlock]', style: { 'line-color': 'var(--block)', 'target-arrow-color': 'var(--block)', 'width': 2.4, 'opacity': 1 } as any },
            { selector: '.hl', style: { 'opacity': 1 } as any },
            { selector: '.dim', style: { 'opacity': 0.15 } as any },
          ],
          wheelSensitivity: 0.2, minZoom: 0.1, maxZoom: 4,
        })
        cy.on('tap', 'node', (e) => onPickNode?.(e.target.id(), e.target.data('props')))
        cy.on('tap', 'edge', (e) => onPickEdge?.(e.target.data('props')))
        cy.on('tap', (e) => { if (e.target === cy) cy.elements().removeClass('hl dim') })
        cyRef.current = cy
        setLoading(false)
      } catch (e: any) {
        if (!dead) { setErr(e?.message || String(e)); setLoading(false) }
      }
    })()
    return () => { dead = true; if (cyRef.current) { cyRef.current.destroy(); cyRef.current = null } }
  }, [focus])

  return (
    <div style={{ position: 'relative', height: '100%', minHeight: 380 }}>
      <div className="row-between" style={{ marginBottom: 8 }}>
        <span className="small dim">{meta ? `${meta.nodes} 节点 · ${meta.edges} 边` : '加载中…'}{focus ? ' · 焦点子图' : ' · 风险优先全局'}</span>
        <span className="row small dim" style={{ gap: 10 }}>
          <Lg c="#18a999" t="源" /><Lg c="#a06ee1" t="汇" /><Lg c="#f5a623" t="Agent" /><Lg c="#4a9bd4" t="工具" /><Lg c="#ff5f56" t="污点/拦截" />
        </span>
      </div>
      {err && <div className="card card-pad small" style={{ borderColor: 'rgba(217,48,37,.4)', color: 'var(--block)' }}>加载失败: {err}</div>}
      <div ref={ref} style={{ height: 420, background: 'var(--bg-1)', borderRadius: 'var(--r-m)', border: '1px solid var(--line)' }} />
      {loading && <Skeleton h={420} style={{ position: 'absolute', top: 30, left: 0, right: 0 }} />}
    </div>
  )
}

function Lg({ c, t }: { c: string; t: string }) {
  return <span className="row" style={{ gap: 4 }}><span style={{ width: 9, height: 9, background: c, borderRadius: 2, display: 'inline-block' }} />{t}</span>
}

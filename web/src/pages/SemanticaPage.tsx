import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import OntologyGraph from '../components/OntologyGraph'
import { EmptyState } from '../components/EmptyState'

/** SemanticaPage — 安全本体图谱页。
 *  三层本体可视化（B 污点血缘 / D 证据链 / E 会话叙事），G6 5.x 渲染 /api/onto/graph。 */
export default function SemanticaPage() {
  const [ask, setAsk] = useState('')
  const [answer, setAnswer] = useState('')
  const [asking, setAsking] = useState(false)
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<any[] | null>(null)
  const [searching, setSearching] = useState(false)

  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const kg = status?.kg || {}
  const ready = kg.graph_ready

  const doAsk = async () => {
    if (!ask.trim() || asking) return
    setAsking(true)
    setAnswer('')
    try {
      const r = await api.kgAsk(ask)
      setAnswer(r?.answer || '(空回答 — KG 数据可能不足)')
    } catch (e: any) {
      setAnswer('问答失败: ' + e.message)
    }
    setAsking(false)
  }

  const doSearch = async () => {
    if (!q.trim() || searching) return
    setSearching(true)
    setHits(null)
    try {
      const r = await api.kgSearch(q)
      setHits(r || [])
    } catch (e: any) {
      setHits([])
    }
    setSearching(false)
  }

  return (
    <div style={{ padding: 22, maxWidth: 1200, margin: '0 auto' }}>
      <div className="row-between" style={{ marginBottom: 14, flexWrap: 'wrap', gap: 10 }}>
        <div>
          <h1 className="h-page">安全知识图谱</h1>
          <div className="small dim">把智能体的每一次高危操作，还原成「谁 · 做了什么 · 碰了哪些敏感资源」的关系图</div>
        </div>
        <div className="row" style={{ gap: 10 }}>
          <span className={`badge ${ready ? 'badge-allow' : 'badge-confirm'}`}>{ready ? '● 实时' : '◐ 构建中'}</span>
          <a className="btn" href="/explorer/" target="_blank" rel="noreferrer">全屏浏览 ↗</a>
        </div>
      </div>

      {/* 图谱 */}
      <div className="card card-pad" style={{ marginBottom: 14 }}>
        <div style={{ height: 520, borderRadius: 8, overflow: 'hidden', border: '1px solid var(--line)' }}>
          <OntologyGraph focus="" />
        </div>
        <div className="small dim" style={{ marginTop: 8 }}>
          <b>蓝圆</b>=智能体 · <b>灰圆</b>=会话 · <b>橙三角</b>=BLOCK裁决 · <b>绿菱形</b>=敏感资源 · 点节点可下钻证据链（D 层）
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        {/* KG 问答 */}
        <div className="card card-pad">
          <div className="h-sec" style={{ marginBottom: 10 }}>问图谱 <span className="dim">(KG-grounded Q&A)</span></div>
          <div className="row" style={{ gap: 8 }}>
            <input
              className="input"
              style={{ flex: 1 }}
              placeholder="问：这个 agent 最近执行了什么？"
              value={ask}
              onChange={(e) => setAsk(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && doAsk()}
            />
            <button className="btn btn-primary" onClick={doAsk} disabled={asking}>{asking ? '…' : '提问'}</button>
          </div>
          {answer && (
            <div className="small" style={{ marginTop: 10, padding: 10, background: 'var(--bg-2)', borderRadius: 6, lineHeight: 1.6 }}>
              {answer}
            </div>
          )}
        </div>

        {/* 语义检索 */}
        <div className="card card-pad">
          <div className="h-sec" style={{ marginBottom: 10 }}>语义检索 <span className="dim">(fastembed)</span></div>
          <div className="row" style={{ gap: 8 }}>
            <input
              className="input"
              style={{ flex: 1 }}
              placeholder="搜：credential / curl / 外发…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && doSearch()}
            />
            <button className="btn btn-primary" onClick={doSearch} disabled={searching}>{searching ? '…' : '搜索'}</button>
          </div>
          {hits && (
            <div style={{ marginTop: 10 }}>
              {hits.length === 0 ? (
                <div className="dim small">无命中（先触发一些事件产生索引）</div>
              ) : (
                hits.map((h: any, i: number) => (
                  <div key={i} style={{ padding: '8px 10px', marginBottom: 6, background: 'var(--bg-2)', borderRadius: 6 }}>
                    <div className="row-between small">
                      <span className="mono">{h.event_id}</span>
                      <span className="dim">{Math.round((h.score || 0) * 100)}%</span>
                    </div>
                    <div className="small dim" style={{ marginTop: 2 }}>{h.text}</div>
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

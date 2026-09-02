import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'
import KGGraph from '../components/KGGraph'

type Tab = 'graph' | 'findings' | 'sessions'

export default function Insight() {
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'graph'
  const focus = params.get('focus') || ''
  const setTab = (t: Tab) => setParams((p) => { p.set('tab', t); return p }, { replace: true })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div className="row-between" style={{ padding: '16px 22px 0' }}>
        <h1 className="h-page" style={{ marginBottom: 10 }}>洞察</h1>
      </div>
      <div className="tabs" style={{ padding: '0 22px' }}>
        <TabBtn id="graph" cur={tab} set={setTab} label="图谱" />
        <TabBtn id="findings" cur={tab} set={setTab} label="发现" />
        <TabBtn id="sessions" cur={tab} set={setTab} label="会话" />
      </div>
      <div style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
        {tab === 'graph' && <GraphLaunch />}
        {tab === 'findings' && <Findings />}
        {tab === 'sessions' && <Sessions focus={focus} />}
      </div>
    </div>
  )
}

function TabBtn({ id, cur, set, label }: { id: Tab; cur: Tab; set: (t: Tab) => void; label: string }) {
  return <button className={`tab ${cur === id ? 'tab-active' : ''}`} onClick={() => set(id)}>{label}</button>
}

/** 本体图谱：内嵌 KGGraph（cytoscape 渲染 /api/onto/graph 统一本体），
 *  同时保留 Semantica Explorer 新窗口入口。 */
function GraphLaunch() {
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const kg = status?.kg || {}
  const ready = kg.graph_ready
  return (
    <div style={{ padding: 22 }}>
      <div className="card card-pad" style={{ marginBottom: 12 }}>
        <div className="row-between" style={{ marginBottom: 10 }}>
          <div>
            <div className="h-sec h-sec-accent">安全知识图谱</div>
            <div className="row" style={{ gap: 10, marginTop: 6 }}>
              <span className={`badge ${ready ? 'badge-allow' : 'badge-confirm'}`}>{ready ? '● 实时' : '◐ 构建中'}</span>
              <span className="small dim">智能体 · 拦截事件 · 敏感资源 的关系全景</span>
            </div>
          </div>
          <a className="btn" href="/explorer/" target="_blank" rel="noreferrer">全屏浏览 →</a>
        </div>
        <div style={{ height: 480, borderRadius: 8, overflow: 'hidden', border: '1px solid var(--line)' }}>
          <KGGraph focus="" />
        </div>
      </div>
      <div className="small dim">
        每个节点是一个真实实体：<b>智能体</b>发起操作，被<b>拦截</b>时留下红色高危事件，并标注它试图触碰的<b>敏感资源</b>（如凭证文件）。点击任意节点即可高亮其完整安全链路。
      </div>
    </div>
  )
}

function Findings() {
  const { data: judge, isLoading: l1 } = useQuery({ queryKey: ['judge'], queryFn: api.judgeFindings })
  const { data: monitor, isLoading: l2 } = useQuery({ queryKey: ['monitor'], queryFn: api.monitorFindings })
  const [open, setOpen] = useState<string | null>(null)

  if (l1 || l2) return <div style={{ padding: 22 }}><SkeletonRows n={4} /></div>
  const j = asList(judge)
  const m = asList(monitor)
  const all = [
    ...j.map((x: any, i: number) => ({ src: 'Judge', x, key: `j${i}` })),
    ...m.map((x: any, i: number) => ({ src: 'Monitor', x, key: `m${i}` })),
  ]

  return (
    <div style={{ padding: 22 }}>
      {all.length === 0 ? (
        <div className="card"><EmptyState icon="✓" title="暂无发现" hint="Judge 与 Monitor 轴尚未产生任何风险发现。" /></div>
      ) : (
        <div className="col" style={{ gap: 8 }}>
          {all.map(({ src, x, key }) => {
            const sev = severity(x)
            const title = (x && (x.title || x.rule || x.kind || x.type)) || 'finding'
            return (
              <div key={key} className="card card-hover" style={{ borderLeft: `3px solid ${sevColor(sev)}` }}>
                <button className="card-pad row" style={{ gap: 10, width: '100%', background: 'none', border: 'none', color: 'inherit', cursor: 'pointer', textAlign: 'left' }}
                  onClick={() => setOpen(open === key ? null : key)}>
                  <span className="badge" style={{ color: sevColor(sev), borderColor: sevColor(sev) + '55', background: sevColor(sev) + '14' }}>{sev}</span>
                  <span className="chip">{src}</span>
                  <span className="small" style={{ flex: 1, fontWeight: 600 }}>{String(title).slice(0, 120)}</span>
                  <span className="dim small">{open === key ? '▲' : '▼'}</span>
                </button>
                {open === key && (
                  <pre className="small mono slide-in" style={{ margin: 0, padding: '0 16px 14px', color: 'var(--fg-1)', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 320, overflow: 'auto' }}>
                    {JSON.stringify(x, null, 2)}
                  </pre>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Sessions({ focus }: { focus: string }) {
  const { data, isLoading } = useQuery({ queryKey: ['sessions'], queryFn: api.sessions, refetchInterval: 8000 })
  const { data: events } = useQuery({ queryKey: ['events'], queryFn: api.events, refetchInterval: 8000 })
  const list = ((data || []) as any[]).sort((a, b) => (b.last_ts || 0) - (a.last_ts || 0))
  const [sel, setSel] = useState<string | null>(focus || null)

  const evs = (events || []) as any[]
  const selEvents = sel ? evs.filter((e: any) => e.SessionID === sel || e.session_id === sel) : []

  return (
    <div style={{ padding: 22, display: 'grid', gridTemplateColumns: sel ? '320px 1fr' : '1fr', gap: 14 }}>
      {/* 会话列表 */}
      <div className="card" style={{ overflow: 'hidden', alignSelf: 'start' }}>
        <div className="card-pad row-between" style={{ borderBottom: '1px solid var(--line)' }}>
          <div className="h-sec">会话</div>
          <span className="small dim">{list.length}</span>
        </div>
        {isLoading ? <SkeletonRows n={5} /> : (
          <div style={{ maxHeight: 520, overflowY: 'auto' }}>
            {list.map((s: any) => {
              const active = sel === s.session_id
              const verdict = (s.last_verdict || '').toLowerCase()
              return (
                <button
                  key={s.session_id}
                  onClick={() => setSel(active ? null : s.session_id)}
                  style={{
                    display: 'block', width: '100%', textAlign: 'left', padding: '10px 14px', cursor: 'pointer',
                    background: active ? 'rgba(22,93,255,.06)' : 'none', border: 'none', borderBottom: '1px solid var(--line)',
                    color: 'inherit',
                  }}
                >
                  <div className="row-between">
                    <span className="small mono" style={{ fontWeight: 600 }}>{String(s.session_id).slice(0, 20)}…</span>
                    <VerdictBadge v={s.last_verdict} />
                  </div>
                  <div className="small dim" style={{ marginTop: 4 }}>
                    {s.events ?? 0} 事件
                    {s.agent_id ? ` · ${s.agent_id}` : ''}
                    {verdict === 'block' && <span style={{ color: 'var(--block)' }}> · ⚠ 有拦截</span>}
                  </div>
                </button>
              )
            })}
          </div>
        )}
        {!isLoading && list.length === 0 && <EmptyState icon="◌" title="暂无会话" />}
      </div>

      {/* 会话详情 */}
      {sel && (
        <div className="card card-pad" style={{ alignSelf: 'start' }}>
          <div className="row-between" style={{ marginBottom: 12 }}>
            <div>
              <div className="h-sec">会话详情</div>
              <div className="small mono dim">{sel}</div>
            </div>
            <span className="small dim">{selEvents.length} 条事件</span>
          </div>
          {selEvents.length === 0 ? (
            <EmptyState icon="◌" title="该会话暂无事件" hint="触发一些工具调用后这里会显示时间线。" />
          ) : (
            <div className="timeline">
              {selEvents.map((e: any, i: number) => {
                const tool = e.Call?.ToolID || e.tool_name || ''
                const verdict = e.Decision?.Final || e.verdict || 'ALLOW'
                const ts = e.Timestamp || e.ts
                const time = ts ? new Date(ts).toLocaleTimeString('zh-CN', { hour12: false }) : ''
                const args = e.Call?.Arguments || e.arguments
                let argText = ''
                try {
                  if (typeof args === 'string') argText = JSON.stringify(JSON.parse(args))?.slice(0, 120)
                  else if (args) argText = JSON.stringify(args)?.slice(0, 120)
                } catch { argText = String(args || '').slice(0, 120) }
                return (
                  <div key={i} className={`timeline-item t-${String(verdict).toLowerCase()}`}>
                    <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
                      <span className="small dim mono">{time}</span>
                      <span className="chip">{tool}</span>
                      <VerdictBadge v={verdict} />
                    </div>
                    {argText && <div className="small dim mono" style={{ marginTop: 3, wordBreak: 'break-all' }}>{argText}</div>}
                    {e.Decision?.Rationale && <div className="small" style={{ color: verdict === 'BLOCK' ? 'var(--block)' : 'var(--fg-2)', marginTop: 2 }}>{e.Decision.Rationale}</div>}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function asList(x: any): any[] {
  if (!x) return []
  if (Array.isArray(x)) return x.filter(Boolean)
  if (Array.isArray(x.findings)) return x.findings.filter(Boolean)
  return [x]
}
function severity(x: any): string {
  if (!x || typeof x !== 'object') return 'low'
  const s = String(x.severity || x.level || x.verdict || '').toLowerCase()
  if (s.includes('block') || s.includes('high') || s.includes('crit')) return 'high'
  if (s.includes('confirm') || s.includes('med') || s.includes('warn')) return 'medium'
  return 'low'
}
function sevColor(s: string) { return s === 'high' ? 'var(--block)' : s === 'medium' ? 'var(--confirm)' : 'var(--fg-2)' }

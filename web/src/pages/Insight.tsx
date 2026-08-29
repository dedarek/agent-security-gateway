import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import KGGraph from '../components/KGGraph'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'

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
        {tab === 'graph' && <KGGraph focus={focus} />}
        {tab === 'findings' && <Findings />}
        {tab === 'sessions' && <Sessions focus={focus} />}
      </div>
    </div>
  )
}

function TabBtn({ id, cur, set, label }: { id: Tab; cur: Tab; set: (t: Tab) => void; label: string }) {
  return <button className={`tab ${cur === id ? 'tab-active' : ''}`} onClick={() => set(id)}>{label}</button>
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
            const title = x.title || x.rule || x.kind || x.type || 'finding'
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
  const list = (data || []) as any[]
  return (
    <div style={{ padding: 22 }}>
      <div className="card" style={{ overflow: 'hidden' }}>
        {isLoading ? <SkeletonRows n={5} /> : (
          <table className="table">
            <thead><tr><th>Session</th><th>事件数</th><th>最终裁决</th></tr></thead>
            <tbody>
              {list.map((s: any) => (
                <tr key={s.session_id} style={focus === s.session_id ? { background: 'rgba(245,166,35,.07)', borderLeft: '3px solid var(--brand)' } : undefined}>
                  <td className="mono small">{s.session_id}</td>
                  <td className="small" style={{ textAlign: 'center' }}>{s.events}</td>
                  <td style={{ textAlign: 'center' }}><VerdictBadge v={s.last_verdict} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {!isLoading && list.length === 0 && <EmptyState icon="◌" title="暂无会话" />}
      </div>
    </div>
  )
}

function asList(x: any): any[] {
  if (!x) return []
  if (Array.isArray(x)) return x
  if (Array.isArray(x.findings)) return x.findings
  return [x]
}
function severity(x: any): string {
  const s = String(x.severity || x.level || x.verdict || '').toLowerCase()
  if (s.includes('block') || s.includes('high') || s.includes('crit')) return 'high'
  if (s.includes('confirm') || s.includes('med') || s.includes('warn')) return 'medium'
  return 'low'
}
function sevColor(s: string) { return s === 'high' ? 'var(--block)' : s === 'medium' ? 'var(--confirm)' : 'var(--fg-2)' }

import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { StatusDot } from '../components/StatusDot'
import { VerdictBadge } from '../components/VerdictBadge'
import { SkeletonRows } from '../components/Skeleton'
import { EmptyState } from '../components/EmptyState'

export default function AgentDetail() {
  const { id } = useParams()
  const agentId = decodeURIComponent(id || '')
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['agent-detail', agentId], queryFn: () => api.agentDetail(agentId), refetchInterval: 4000 })
  const { data: history } = useQuery({ queryKey: ['history', agentId], queryFn: () => api.agentHistory(agentId) })
  const [confirmDel, setConfirmDel] = useState(false)
  const del = useMutation({
    mutationFn: () => api.deleteAgent(agentId),
    onSuccess: () => { window.location.href = '/fleet' },
  })

  if (isLoading) return <div style={{ padding: 22 }}><SkeletonRows n={6} /></div>
  if (!data) return <EmptyState icon="✕" title="Agent 不存在" action={<Link className="btn" to="/fleet">返回舰队</Link>} />

  const a = data.agent
  const chain = data.chain || []
  const sessions = data.sessions || []

  return (
    <div style={{ padding: 22, maxWidth: 980 }}>
      <Link to="/fleet" className="small dim">← 返回舰队</Link>
      <div className="row" style={{ gap: 10, margin: '8px 0 4px' }}>
        <h1 className="h-page" style={{ margin: 0 }}>{a.alias || a.agent_id}</h1>
        <StatusDot status={a.status} />
        <span className="small muted">{a.status}</span>
        {a.observed_model
          ? <span className="badge badge-allow">gateway-observed</span>
          : <span className="badge" style={{ color: 'var(--fg-2)', borderColor: 'var(--line)' }}>self-reported</span>}
      </div>
      <div className="small dim mono" style={{ marginBottom: 16 }}>
        {a.agent_id} · {a.agent_type || 'unknown'} · {a.model || '无模型'} {a.provider ? `(${a.provider})` : ''} · {a.ip || '-'}
      </div>

      <div className="row" style={{ gap: 10, marginBottom: 18 }}>
        <button className="btn" onClick={() => qc.invalidateQueries({ queryKey: ['agent-detail'] })}>刷新</button>
        {confirmDel
          ? <button className="btn btn-danger" onClick={() => del.mutate()}>确认删除（仅离线可删）</button>
          : <button className="btn btn-ghost" onClick={() => setConfirmDel(true)}>删除 Agent…</button>}
      </div>

      <h2 className="h-sec" style={{ marginBottom: 8 }}>工作链路 <span className="dim">({chain.length} 步)</span></h2>
      {chain.length === 0 ? (
        <div className="card"><EmptyState icon="◌" title="暂无链路" hint="等待 hook 上报工具调用。" /></div>
      ) : (
        <div className="card card-pad">
          <div className="timeline">
            {chain.slice().reverse().map((s: any, i: number) => (
              <div key={i} className={`timeline-item t-${(s.verdict || 'ALLOW').toLowerCase()}`}>
                <div className="row" style={{ gap: 10, flexWrap: 'wrap' }}>
                  <span className="small dim mono">{s.at ? new Date(s.at).toLocaleTimeString('zh-CN', { hour12: false }) : '-'}</span>
                  <span className="chip">{s.tool || s.kind}</span>
                  <VerdictBadge v={s.verdict} />
                </div>
                <div className="small" style={{ color: 'var(--fg-1)', marginTop: 4, wordBreak: 'break-all' }}>{s.summary || '-'}</div>
                {s.reason && <div className="small" style={{ color: 'var(--block)', marginTop: 2 }}>{s.reason}</div>}
              </div>
            ))}
          </div>
        </div>
      )}

      <h2 className="h-sec" style={{ margin: '18px 0 8px' }}>模型变更历史</h2>
      <div className="card card-pad">
        {history?.history?.length ? history.history.map((h: any, i: number) => (
          <div key={i} className="small" style={{ padding: '5px 0', borderBottom: '1px solid rgba(35,45,59,.5)' }}>
            <span className="dim mono">{new Date(h.at).toLocaleString('zh-CN')}</span> — {h.from || '(none)'} → <b>{h.to}</b> <span className="dim">({h.source})</span>
          </div>
        )) : <span className="dim small">无变更</span>}
      </div>

      <h2 className="h-sec" style={{ margin: '18px 0 8px' }}>会话</h2>
      <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
        {sessions.map((s: any) => (
          <Link key={s.session_id} to={`/insight?tab=sessions&focus=${encodeURIComponent(s.session_id)}`} className="chip" style={{ textDecoration: 'none' }}>
            {s.session_id} <span className="dim">({s.event_count})</span>
          </Link>
        ))}
        {sessions.length === 0 && <span className="dim small">暂无会话</span>}
      </div>
    </div>
  )
}

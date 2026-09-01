import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { StatusDot } from '../components/StatusDot'
import { BrandLogo, logoFor } from '../assets/logos'
import { VerdictBadge } from '../components/VerdictBadge'
import { SkeletonRows } from '../components/Skeleton'
import { EmptyState } from '../components/EmptyState'

export default function AgentDetail() {
  const { id } = useParams()
  const agentId = decodeURIComponent(id || '')
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['agent-detail', agentId], queryFn: () => api.agentDetail(agentId), refetchInterval: 4000 })
  const { data: history } = useQuery({ queryKey: ['history', agentId], queryFn: () => api.agentHistory(agentId) })
  const { data: dataAccess } = useQuery({ queryKey: ['data-access', agentId], queryFn: () => api.dataAccess(agentId), refetchInterval: 4000 })
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
  const agentLogoKey = a.agent_type || a.agent_id
  const reportedIP = a.ip || a.observed_ips?.[0]
  const connectionIP = a.connection_ip
  const valueOrMissing = (value?: string) => value || '未上报'

  return (
    <div style={{ padding: 22, maxWidth: 980 }}>
      <Link to="/fleet" className="small dim">← 返回舰队</Link>
      <div className="row" style={{ gap: 10, margin: '8px 0 4px' }}>
        {logoFor(agentLogoKey) && <BrandLogo name={agentLogoKey} size={26} />}
        <h1 className="h-page" style={{ margin: 0 }}>{a.alias || a.agent_id}</h1>
        <StatusDot status={a.status} />
        {a.observed_model
          ? <span className="badge badge-allow">gateway-observed</span>
          : <span className="badge" style={{ color: 'var(--fg-2)', borderColor: 'var(--line)' }}>self-reported</span>}
      </div>
      <div className="card card-pad" style={{ margin: '12px 0 18px' }}>
        <div className="kv">
          <dt>Agent ID</dt>
          <dd className="mono">{valueOrMissing(a.agent_id)}</dd>
          <dt>类型</dt>
          <dd className="row" style={{ gap: 6 }}>
            {logoFor(agentLogoKey) && <BrandLogo name={agentLogoKey} size={16} />}
            <span>{valueOrMissing(a.agent_type)}</span>
          </dd>
          <dt>机器名称</dt>
          <dd className="mono">{valueOrMissing(a.machine_name)}</dd>
          <dt>IP</dt>
          <dd className="mono">{valueOrMissing(reportedIP)}</dd>
          {connectionIP && connectionIP !== reportedIP && <>
            <dt>连接 IP</dt>
            <dd className="mono">{connectionIP}</dd>
          </>}
          <dt>模型</dt>
          <dd>{a.model ? <span className="row" style={{ gap: 6 }}><BrandLogo name={a.model} size={16} />{a.model}</span> : '未上报'}</dd>
          <dt>Provider</dt>
          <dd>{valueOrMissing(a.provider)}</dd>
        </div>
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

      <h2 className="h-sec" style={{ margin: '18px 0 8px' }}>数据流 <span className="dim">(DataAccess · 数据血缘)</span></h2>
      <div className="card card-pad">
        {dataAccess?.hops?.length ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {dataAccess.hops.slice(0, 30).map((h: any, i: number) => (
              <div key={i} className={`timeline-item t-${(h.decision || 'ALLOW').toLowerCase()}`} style={{ padding: '6px 8px', borderRadius: 6 }}>
                <div className="row" style={{ gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
                  <span className="small dim mono">{h.at ? new Date(h.at).toLocaleTimeString('zh-CN', { hour12: false }) : '-'}</span>
                  <span className="chip mono">{h.tool_id}</span>
                  <span className="chip" style={{ color: 'var(--primary)' }}>{h.operation}</span>
                  {h.source && <span className="small mono dim">← {h.source}</span>}
                  {h.destination && <span className="small mono" style={{ color: h.trust_zone_dst === 'external' ? 'var(--block)' : 'inherit' }}>→ {h.destination}</span>}
                  {h.data_class && <span className="badge" style={{ color: 'var(--warn)', borderColor: 'var(--warn)' }}>{h.data_class}</span>}
                  {h.trust_zone_dst === 'external' && <span className="badge" style={{ color: 'var(--block)', borderColor: 'var(--block)' }}>external</span>}
                  <VerdictBadge v={h.decision} />
                </div>
                {h.taint_tags?.length > 0 && (
                  <div className="small dim" style={{ marginTop: 3, wordBreak: 'break-all' }}>
                    taint: {h.taint_tags.join(', ')}
                  </div>
                )}
              </div>
            ))}
          </div>
        ) : <span className="dim small">暂无数据流事件（工具调用经过网关后出现）</span>}
        {dataAccess?.lineage?.length > 0 && (
          <div style={{ marginTop: 10 }}>
            <div className="small dim" style={{ marginBottom: 4 }}>血缘路径</div>
            {dataAccess.lineage.map((p: string[], i: number) => (
              <div key={i} className="small mono" style={{ color: 'var(--fg-1)', wordBreak: 'break-all' }}>{p.join(' → ')}</div>
            ))}
          </div>
        )}
      </div>

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

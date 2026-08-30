import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Agent } from '../lib/types'
import { StatusDot } from '../components/StatusDot'
import { BrandLogo, logoFor } from '../assets/logos'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'

export default function Fleet() {
  const { data: agents, isLoading } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 10000 })
  const list: Agent[] = (agents || []).slice().sort((a, b) => rank(a.status) - rank(b.status))

  return (
    <div style={{ padding: 22 }}>
      <h1 className="h-page">舰队</h1>
      <div className="small dim" style={{ marginBottom: 14 }}>所有已注册 Agent，按状态排序（active → idle → offline）</div>
      <div className="card" style={{ overflow: 'hidden' }}>
        {isLoading ? <SkeletonRows n={6} /> : (
          <table className="table">
            <thead>
              <tr><th>Agent</th><th>状态</th><th>模型</th><th>来源</th><th>会话</th><th>IP</th><th>最后活动</th><th></th></tr>
            </thead>
            <tbody>
              {list.map((a) => (
                <tr key={a.agent_id}>
                  <td>
                    <div style={{ fontWeight: 600 }}>{a.alias || a.agent_id}</div>
                    <div className="small dim mono">{a.agent_id}{a.agent_type ? ` · ${a.agent_type}` : ''}</div>
                  </td>
                  <td><StatusDot status={a.status} /></td>
                  <td className="small"><span className="row" style={{ gap: 6 }}>{logoFor(a.model || a.provider || '') ? <BrandLogo name={(a.model || a.provider)!} size={16} /> : null}{a.model || '-'}{a.provider && a.provider !== a.model ? <span className="dim"> ({a.provider})</span> : ''}</span></td>
                  <td>{(a as any).observed_model ? <span className="badge badge-allow">observed</span> : <span className="badge" style={{ color: 'var(--fg-2)', borderColor: 'var(--line)' }}>self-reported</span>}</td>
                  <td className="small">{a.session_count ?? a.session_ids?.length ?? 0}</td>
                  <td className="small dim mono">{a.ip || '-'}</td>
                  <td className="small dim">{a.last_activity ? new Date(a.last_activity).toLocaleString('zh-CN') : '-'}</td>
                  <td><Link className="btn btn-ghost" style={{ padding: '3px 10px', fontSize: 11 }} to={`/fleet/${encodeURIComponent(a.agent_id)}`}>详情</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {!isLoading && list.length === 0 && (
          <EmptyState icon="◇" title="暂无已注册 Agent" hint="按 docs/ONBOARDING.md 用一行命令接入；注册成功后此处自动出现。" />
        )}
      </div>
    </div>
  )
}

function rank(s: string) { return s === 'active' ? 0 : s === 'idle' ? 1 : 2 }

import { Link } from 'react-router-dom'
import { StatusDot } from './StatusDot'
import type { Agent } from '../lib/types'

export function AgentCard({ a }: { a: Agent }) {
  const observed = (a as any).observed_model
  return (
    <Link to={`/fleet/${encodeURIComponent(a.agent_id)}`} style={{ textDecoration: 'none', color: 'inherit' }}>
      <div className="card card-hover card-pad col" style={{ minWidth: 220 }}>
        <div className="row-between">
          <div style={{ fontWeight: 700 }}>{a.alias || a.agent_id}</div>
          <span className="row" style={{ gap: 6 }}>
            <StatusDot status={a.status} />
            <span className="small muted">{a.status}</span>
          </span>
        </div>
        <div className="small dim mono">{a.agent_id}</div>
        <div className="row small" style={{ gap: 6, flexWrap: 'wrap' }}>
          {a.model && <span className="chip">{a.model}</span>}
          {a.provider && <span className="chip">{a.provider}</span>}
          {observed ? <span className="chip" style={{ color: 'var(--allow)' }}>gateway-observed</span>
            : a.model ? <span className="chip">self-reported</span> : null}
        </div>
        <div className="small dim">{a.session_count ?? a.session_ids?.length ?? 0} 会话 · {a.ip || '-'}</div>
      </div>
    </Link>
  )
}

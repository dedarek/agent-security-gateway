import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

/** ApprovalQueue — 待人工审批卡（CONFIRM 决策 → ask → 批准/拒绝）。 */
export default function ApprovalQueue() {
  const qc = useQueryClient()
  const { data: pending = [] } = useQuery({ queryKey: ['approvals'], queryFn: api.approvals, refetchInterval: 4000 })

  const decide = useMutation({
    mutationFn: ({ id, approve }: { id: string; approve: boolean }) => api.approvalDecide(id, approve),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['approvals'] }),
  })

  if (pending.length === 0) return null

  return (
    <div className="card card-pad" style={{ marginBottom: 12, borderColor: 'var(--confirm)', borderWidth: 1, background: 'rgba(255,125,0,.03)' }}>
      <div className="row-between" style={{ marginBottom: 8 }}>
        <div className="h-sec" style={{ color: 'var(--confirm)' }}>⏳ 待人工审批 <span className="dim">({pending.length})</span></div>
        <span className="small dim">CONFIRM 决策 — 批准后工具继续，拒绝则终止</span>
      </div>
      {pending.map((p: any) => (
        <div key={p.ID} className="row" style={{ gap: 10, padding: '8px 10px', background: 'var(--bg-1)', borderRadius: 6, marginBottom: 6, flexWrap: 'wrap' }}>
          <span className="chip">{p.ToolID || 'tool'}</span>
          <span className="small" style={{ flex: 1, minWidth: 180 }}>{p.Reason || '需要审批'}</span>
          <span className="small dim mono">{p.ID}</span>
          <span className="small dim">risk {p.Risk ?? '?'}</span>
          <div className="row" style={{ gap: 6 }}>
            <button
              className="btn"
              style={{ color: 'var(--allow)', borderColor: 'var(--allow)', padding: '2px 10px', fontSize: 12 }}
              onClick={() => decide.mutate({ id: p.ID, approve: true })}
            >✓ 批准</button>
            <button
              className="btn"
              style={{ color: 'var(--block)', borderColor: 'var(--block)', padding: '2px 10px', fontSize: 12 }}
              onClick={() => decide.mutate({ id: p.ID, approve: false })}
            >✕ 拒绝</button>
          </div>
        </div>
      ))}
    </div>
  )
}

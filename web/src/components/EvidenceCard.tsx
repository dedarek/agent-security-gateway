import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { Skeleton } from './Skeleton'

/** D layer — one decision's engine-vote matrix + taint lineage note. */
export function EvidenceCard({ eventId }: { eventId: string }) {
  const { data, isLoading, error } = useQuery({ queryKey: ['evidence', eventId], queryFn: () => api.ontoEvidence(eventId), enabled: !!eventId })
  if (!eventId) return null
  if (isLoading) return <Skeleton h={180} />
  if (error || !data) return <div className="small" style={{ color: 'var(--block)' }}>证据加载失败</div>

  const votes: any[] = data.votes || []
  const voteColor = (v: string) => v === 'BLOCK' ? 'var(--block)' : v === 'CONFIRM' ? 'var(--confirm)' : 'var(--allow)'

  return (
    <div className="col" style={{ gap: 12 }}>
      <div className="row-between">
        <span className={`badge ${data.final === 'BLOCK' ? 'badge-block' : 'badge-confirm'}`}>{data.final} · risk {data.risk}</span>
        {data.sole_axis && <span className="chip" style={{ color: 'var(--confirm)', borderColor: 'rgba(227,116,0,.4)' }}>单一引擎撑起</span>}
      </div>

      <div className="col" style={{ gap: 7 }}>
        <div className="h-sec">引擎投票（{votes.length}）</div>
        {votes.map((v, i) => (
          <div key={i}>
            <div className="row-between" style={{ marginBottom: 2 }}>
              <span className="small mono" style={{ fontWeight: 600 }}>{v.engine}</span>
              <span className="row" style={{ gap: 6 }}>
                <span className="chip" style={{ fontSize: 9 }}>axis{v.axis}</span>
                <span className="small" style={{ color: voteColor(v.vote), fontWeight: 700 }}>{v.vote}</span>
                <span className="small dim mono">{v.score}</span>
              </span>
            </div>
            <div style={{ height: 6, background: 'var(--bg-3)', borderRadius: 3, overflow: 'hidden' }}>
              <div style={{ height: '100%', width: `${(v.score / 100) * 100}%`, background: voteColor(v.vote), borderRadius: 3, transition: 'width 500ms var(--ease)' }} />
            </div>
            {v.reasons?.[0] && <div className="small dim" style={{ marginTop: 2, wordBreak: 'break-all' }}>{v.reasons[0]}</div>}
          </div>
        ))}
      </div>

      {data.taint_from && (
        <div className="card card-pad" style={{ background: 'var(--bg-2)' }}>
          <div className="h-sec" style={{ marginBottom: 4 }}>污点血缘</div>
          <div className="small mono" style={{ wordBreak: 'break-all' }}>{data.taint_from}</div>
        </div>
      )}
      {data.sole_axis && (
        <div className="small" style={{ color: 'var(--confirm)', background: 'rgba(227,116,0,.07)', padding: '8px 10px', borderRadius: 'var(--r-s)' }}>
          可解释性：此 BLOCK 由单一引擎驱动，调整其权重会直接改变结论。
        </div>
      )}
    </div>
  )
}

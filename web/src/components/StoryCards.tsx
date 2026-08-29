export type Story = {
  session_id: string
  agent: string
  steps: number
  peak_risk: number
  outcome: string
  last: string
  phases: { phase: string; tool: string; verdict: string }[]
  timeline: { at: string; tool: string; verdict: string; summary: string }[]
}

const PHASE_COLOR: Record<string, string> = {
  recon: '#4a9bd4', collect: '#f5a623', exfil: '#e37400', blocked: '#d93025',
}

/** E layer — session narrative cards. Each card is one session's story with a
 * phase strip (recon→collect→exfil→blocked). BLOCK cards float to the top. */
export function StoryCards({ stories, onOpen }: { stories: Story[]; onOpen?: (s: Story) => void }) {
  if (!stories.length) {
    return <div className="empty"><div className="empty-icon">◌</div><div className="muted">暂无会话故事</div></div>
  }
  return (
    <div className="col" style={{ gap: 10 }}>
      {stories.map((s) => {
        const blocked = s.outcome === 'blocked'
        return (
          <button key={s.session_id} onClick={() => onOpen?.(s)}
            className="card card-hover card-pad"
            style={{
              textAlign: 'left', cursor: 'pointer', color: 'inherit', font: 'inherit', width: '100%',
              borderLeft: `4px solid ${blocked ? 'var(--block)' : 'var(--line)'}`,
              background: blocked ? 'rgba(217,48,37,.04)' : 'var(--bg-1)',
            }}>
            <div className="row-between" style={{ marginBottom: 6 }}>
              <span className="row" style={{ gap: 8, minWidth: 0 }}>
                <span className={`badge ${blocked ? 'badge-block' : 'badge-allow'}`}>{blocked ? 'BLOCK' : 'CLEAN'}</span>
                <span className="mono small" style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis' }}>{s.session_id}</span>
              </span>
              <span className="chip" style={{ color: blocked ? 'var(--block)' : 'var(--fg-1)' }}>risk {s.peak_risk}</span>
            </div>
            <div className="small dim" style={{ marginBottom: 8 }}>{s.agent} · {s.steps} 步 · {s.last}</div>
            {/* phase strip */}
            <div className="row" style={{ gap: 3, marginBottom: 8 }}>
              {s.phases.map((p, i) => (
                <span key={i} title={`${p.phase} · ${p.tool} · ${p.verdict}`}
                  style={{ flex: 1, height: 8, borderRadius: 3, background: PHASE_COLOR[p.phase] || 'var(--bg-3)', opacity: p.verdict === 'BLOCK' ? 1 : 0.6 }} />
              ))}
            </div>
            <div className="small" style={{ color: 'var(--fg-1)' }}>
              {s.timeline.slice(0, 4).map((t, i) => (
                <span key={i}>
                  {i > 0 && <span className="dim"> → </span>}
                  <span style={{ color: t.verdict === 'BLOCK' ? 'var(--block)' : undefined }}>{t.tool}</span>
                </span>
              ))}
              {s.timeline.length > 4 && <span className="dim"> …</span>}
            </div>
          </button>
        )
      })}
    </div>
  )
}

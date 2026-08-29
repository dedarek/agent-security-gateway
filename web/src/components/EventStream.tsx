import { useEffect, useRef, useState } from 'react'
import type { StreamStep } from '../lib/sse'
import { VerdictBadge } from './VerdictBadge'

const MAX = 200

/**
 * Live event feed. Steps arrive via SSE push; newest on top; BLOCK rows get
 * a one-shot red glow. Clicking a row opens the detail drawer (handled by
 * the parent via onSelect).
 */
export function EventStream({ steps, onSelect, live }: {
  steps: StreamStep[]
  onSelect?: (s: StreamStep) => void
  live: boolean
}) {
  const [flashKey, setFlashKey] = useState<string | null>(null)
  const lastTop = useRef<string | null>(null)

  useEffect(() => {
    const top = steps[0]
    if (!top) return
    const key = top.at + top.agent_id + top.tool_name
    if (lastTop.current && key !== lastTop.current && top.verdict === 'BLOCK') {
      setFlashKey(key)
      const t = setTimeout(() => setFlashKey(null), 700)
      return () => clearTimeout(t)
    }
    lastTop.current = key
  }, [steps])

  return (
    <div className="card" style={{ overflow: 'hidden', display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <div className="card-pad row-between" style={{ borderBottom: '1px solid var(--line)', paddingBottom: 10 }}>
        <span className="h-sec">实时事件流</span>
        <span className="row small" style={{ gap: 6 }}>
          <span className={`health-dot ${live ? 'health-ok' : 'health-bad'}`} />
          <span className="muted">{live ? 'SSE 实时' : 'SSE 断开 · 轮询降级'}</span>
        </span>
      </div>
      <div style={{ overflowY: 'auto', maxHeight: 420 }}>
        {steps.length === 0 && (
          <div className="empty" style={{ padding: 28 }}>
            <div className="empty-icon">◌</div>
            <div className="muted">等待 hook 上报…</div>
          </div>
        )}
        {steps.slice(0, MAX).map((s, i) => {
          const key = s.at + s.agent_id + s.tool_name + i
          const isBlock = s.verdict === 'BLOCK'
          return (
            <div
              key={key}
              className={`row slide-in ${flashKey === key ? 'block-flash' : ''}`}
              onClick={() => onSelect?.(s)}
              style={{
                padding: '9px 14px', gap: 10, cursor: onSelect ? 'pointer' : 'default',
                borderBottom: '1px solid rgba(35,45,59,.5)',
                borderLeft: isBlock ? '3px solid var(--block)' : s.verdict === 'CONFIRM' ? '3px solid var(--confirm)' : '3px solid transparent',
                background: isBlock ? 'rgba(255,95,86,.05)' : undefined,
              }}
            >
              <span className="small dim mono" style={{ minWidth: 74, flexShrink: 0 }}>
                {s.at ? new Date(s.at).toLocaleTimeString('zh-CN', { hour12: false }) : '--:--:--'}
              </span>
              <VerdictBadge v={s.verdict} />
              <span className="chip" style={{ flexShrink: 0 }}>{s.tool_name || s.kind}</span>
              <span className="small" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--fg-1)' }}>
                {s.summary || s.reason || '-'}
              </span>
              <span className="small dim mono" style={{ flexShrink: 0, maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis' }}>{s.agent_id}</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

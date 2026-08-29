import { useState } from 'react'
import type { StreamStep } from '../lib/sse'
import { VerdictBadge } from './VerdictBadge'

/** VerdictBreakdown — the "security in detail" panel: BLOCK / CONFIRM / ALLOW
 * each as its own drill-down list with per-event engine signals (which axis
 * fired, with what score and reason). This is the SOC gene: every verdict is
 * explained, not just counted. */
export function VerdictBreakdown({ steps, onTrace }: {
  steps: StreamStep[]
  onTrace?: (s: StreamStep) => void
}) {
  const [tab, setTab] = useState<'BLOCK' | 'CONFIRM' | 'ALLOW'>('BLOCK')
  const groups = {
    BLOCK: steps.filter((s) => s.verdict === 'BLOCK'),
    CONFIRM: steps.filter((s) => s.verdict === 'CONFIRM'),
    ALLOW: steps.filter((s) => s.verdict === 'ALLOW'),
  }
  const cur = groups[tab]

  return (
    <div className="card" style={{ overflow: 'hidden' }}>
      <div className="card-pad" style={{ borderBottom: '1px solid var(--line)', paddingBottom: 0 }}>
        <div className="tabs" style={{ borderBottom: 'none' }}>
          <TabBtn id="BLOCK" cur={tab} set={setTab} n={groups.BLOCK.length} color="var(--block)" label="拦截" />
          <TabBtn id="CONFIRM" cur={tab} set={setTab} n={groups.CONFIRM.length} color="var(--confirm)" label="警告" />
          <TabBtn id="ALLOW" cur={tab} set={setTab} n={groups.ALLOW.length} color="var(--allow)" label="放行" />
        </div>
      </div>
      <div style={{ maxHeight: 320, overflowY: 'auto' }}>
        {cur.length === 0 && (
          <div className="empty" style={{ padding: 24 }}>
            <div className="empty-icon">{tab === 'BLOCK' ? '🛡' : tab === 'CONFIRM' ? '⚠' : '✓'}</div>
            <div className="muted small">{tab === 'BLOCK' ? '暂无拦截 — 系统平稳' : tab === 'CONFIRM' ? '暂无待确认' : '暂无放行记录'}</div>
          </div>
        )}
        {cur.slice(0, 80).map((s, i) => (
          <VerdictRow key={i} s={s} onTrace={onTrace} />
        ))}
      </div>
    </div>
  )
}

function TabBtn({ id, cur, set, n, color, label }: any) {
  return (
    <button className={`tab ${cur === id ? 'tab-active' : ''}`} onClick={() => set(id)} style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
      <span style={{ width: 7, height: 7, borderRadius: 2, background: color, display: 'inline-block' }} />
      {label}
      <span className="chip" style={{ padding: '0 6px', fontSize: 10 }}>{n}</span>
    </button>
  )
}

function VerdictRow({ s, onTrace }: { s: StreamStep; onTrace?: (s: StreamStep) => void }) {
  const [open, setOpen] = useState(false)
  const isBlock = s.verdict === 'BLOCK'
  return (
    <div style={{ borderBottom: '1px solid rgba(35,45,59,.4)', borderLeft: `3px solid ${isBlock ? 'var(--block)' : s.verdict === 'CONFIRM' ? 'var(--confirm)' : 'var(--allow)'}` }}>
      <button
        onClick={() => setOpen(!open)}
        style={{ width: '100%', background: 'none', border: 'none', padding: '9px 14px', cursor: 'pointer', textAlign: 'left', color: 'inherit', display: 'flex', gap: 10, alignItems: 'center' }}
      >
        <VerdictBadge v={s.verdict} />
        <span className="chip">{s.tool_name || s.kind}</span>
        <span className="small" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--fg-1)' }}>
          {s.summary || s.reason || '-'}
        </span>
        <span className="small dim mono" style={{ flexShrink: 0 }}>{s.at ? new Date(s.at).toLocaleTimeString('zh-CN', { hour12: false }) : ''}</span>
        <span className="dim" style={{ fontSize: 10 }}>{open ? '▲' : '▼'}</span>
      </button>
      {open && (
        <div className="slide-in" style={{ padding: '0 14px 12px', background: 'var(--bg-2)' }}>
          {s.reason && (
            <div className="small" style={{ color: isBlock ? 'var(--block)' : 'var(--fg-1)', marginBottom: 8, wordBreak: 'break-all' }}>
              <b>判定：</b>{s.reason}
            </div>
          )}
          <div className="kv" style={{ marginBottom: 8 }}>
            <dt>Agent</dt><dd className="mono">{s.agent_id}</dd>
            <dt>Session</dt><dd className="mono">{s.session_id || '-'}</dd>
            <dt>时间</dt><dd className="mono">{s.at ? new Date(s.at).toLocaleString('zh-CN') : '-'}</dd>
          </div>
          {isBlock && onTrace && (
            <button className="btn btn-primary" style={{ fontSize: 11, padding: '4px 12px' }} onClick={() => onTrace(s)}>
              在图谱中追溯血缘 →
            </button>
          )}
        </div>
      )}
    </div>
  )
}

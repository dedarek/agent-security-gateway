import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

/** ProtectionStatus — Kill Switch + Enforcement Coverage 首页卡。
 * 顶部：agent 保护状态 + 模式切换（NORMAL/QUARANTINE/KILL）
 * 中部：六维健康（LLM Proxy / Hook PEP / Cedar / DLP / Taint / KG）
 * 底部：Enforcement Coverage % 条 + 两个大红按钮
 * degraded 时整卡变红 + SECURITY DEGRADED 横幅。 */
export default function ProtectionStatus({ agentId = 'local-claude-code' }: { agentId?: string }) {
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState<'quarantine' | 'kill' | null>(null)

  const { data: cov } = useQuery({ queryKey: ['coverage'], queryFn: api.coverage, refetchInterval: 3000 })
  const { data: modeData } = useQuery({ queryKey: ['agent-mode', agentId], queryFn: () => api.agentMode(agentId), refetchInterval: 5000 })

  const mode = modeData?.mode || 'normal'
  const degraded = cov?.degraded || false
  const pct = cov?.coverage_pct ?? 0
  const issues: string[] = cov?.issues || []
  const agent = cov?.agents?.find((a: any) => a.agent_id === agentId)
  const status = agent?.status || 'protected'

  const setMode = useMutation({
    mutationFn: (m: string) => api.setAgentMode(agentId, m),
    onSuccess: () => {
      setConfirming(null)
      qc.invalidateQueries({ queryKey: ['agent-mode'] })
      qc.invalidateQueries({ queryKey: ['coverage'] })
    },
  })

  const doSwitch = (m: 'quarantine' | 'kill') => {
    if (confirming === m) {
      setMode.mutate(m)
    } else {
      setConfirming(m)
    }
  }

  const dim = status === 'protected' ? 'var(--allow)' : 'var(--block)'
  const statusText = status === 'protected' ? 'PROTECTED' : status === 'stale' ? 'OFFLINE' : 'DEGRADED'

  return (
    <div className="card card-pad" style={{ border: `1px solid ${degraded ? 'var(--block)' : 'var(--line)'}`, background: degraded ? 'rgba(217,48,37,.03)' : undefined }}>
      {/* 顶行：agent 状态 + 模式切换 */}
      <div className="row-between" style={{ marginBottom: 14, flexWrap: 'wrap', gap: 8 }}>
        <div className="row" style={{ gap: 10, alignItems: 'center' }}>
          <span style={{ fontWeight: 600, fontSize: 15 }}>Claude Code</span>
          <span className={`badge ${degraded ? 'badge-block' : 'badge-allow'}`} style={{ color: dim, borderColor: dim }}>{statusText}</span>
          {degraded && <span className="badge badge-block">⚠ SECURITY DEGRADED</span>}
        </div>
        <div className="row" style={{ gap: 8, alignItems: 'center' }}>
          <span className="small dim">Protection Mode</span>
          <div className="seg">
            {(['normal', 'quarantine', 'kill'] as const).map((m) => (
              <button
                key={m}
                className={`seg-item ${mode === m ? 'on' : ''}`}
                style={mode === m ? { color: m === 'kill' ? 'var(--block)' : m === 'quarantine' ? 'var(--warn)' : undefined } : undefined}
                onClick={() => m !== mode && doSwitch(m)}
              >
                {m === 'normal' ? '● NORMAL' : m === 'quarantine' ? '◐ QUARANTINE' : '✕ KILL'}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* degraded 横幅 */}
      {degraded && (
        <div className="row" style={{ gap: 8, marginBottom: 10, flexWrap: 'wrap' }}>
          {issues.map((iss: string, i: number) => (
            <span key={i} className="badge badge-block">{iss}</span>
          ))}
        </div>
      )}

      {/* 六维健康 */}
      <div className="row" style={{ gap: 6, marginBottom: 12, flexWrap: 'wrap' }}>
        {[
          { k: 'LLM Proxy', ok: agent?.proxy_up },
          { k: 'Hook PEP', ok: agent?.hook_configured },
          { k: 'Cedar', ok: agent?.policy_engine },
          { k: 'DLP', ok: agent?.dlp },
          { k: 'Taint', ok: true },
          { k: 'KG', ok: agent?.kg },
          { k: 'PostToolUse', ok: agent?.posthook_configured, partial: true },
        ].map((c) => (
          <span key={c.k} className="small" style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 8px', borderRadius: 10, background: 'var(--bg-2)' }}>
            <span style={{ color: c.ok ? 'var(--allow)' : c.partial ? 'var(--warn)' : 'var(--block)' }}>{c.ok ? '●' : c.partial ? '◐' : '○'}</span>
            {c.k}
          </span>
        ))}
      </div>

      {/* Coverage 条 + 按钮 */}
      <div className="row" style={{ gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
        <div style={{ flex: 1, minWidth: 180 }}>
          <div className="row-between small dim" style={{ marginBottom: 4 }}>
            <span>Enforcement Coverage</span>
            <span>{pct}%</span>
          </div>
          <div style={{ height: 8, background: 'var(--bg-2)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{ width: `${pct}%`, height: '100%', background: degraded ? 'var(--block)' : 'var(--allow)', borderRadius: 4, transition: 'width .4s' }} />
          </div>
        </div>
        <div className="row" style={{ gap: 8 }}>
          <button
            className="btn"
            style={confirming === 'quarantine' ? { background: 'var(--warn)', color: '#fff', borderColor: 'var(--warn)' } : { color: 'var(--warn)', borderColor: 'var(--warn)' }}
            onClick={() => doSwitch('quarantine')}
          >
            {confirming === 'quarantine' ? '确认 QUARANTINE?' : '◐ QUARANTINE AGENT'}
          </button>
          <button
            className="btn"
            style={confirming === 'kill' ? { background: 'var(--block)', color: '#fff', borderColor: 'var(--block)' } : { color: 'var(--block)', borderColor: 'var(--block)' }}
            onClick={() => doSwitch('kill')}
          >
            {confirming === 'kill' ? '确认 KILL?' : '✕ KILL AGENT'}
          </button>
          {mode !== 'normal' && (
            <button className="btn" onClick={() => doSwitch('normal')}>恢复 NORMAL</button>
          )}
        </div>
      </div>
    </div>
  )
}

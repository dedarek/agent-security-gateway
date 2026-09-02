import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { BrandLogo, logoFor } from '../assets/logos'

/**
 * FleetProtection — 概览页顶部的「防护总览」条。
 * 层级修正: 不再把单个 agent 当 hero 卡（那会造成"为什么只有 Claude Code 的困惑"），
 * 而是平铺展示所有已接入 agent 的保护状态 + 模式切换 + 整体覆盖率。
 * 每个 agent 一行: logo | 名称 | 状态 | 模式切换(正常/限制/停用) | 详情。
 */
export default function FleetProtection() {
  const qc = useQueryClient()
  const nav = useNavigate()

  const { data: agents } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 8000 })
  const { data: cov } = useQuery({ queryKey: ['coverage'], queryFn: api.coverage, refetchInterval: 3000 })

  const list: any[] = agents || []
  const real = list.filter((a: any) => a.status !== 'offline')
  const degraded = cov?.degraded || false
  const pct = cov?.coverage_pct ?? 0
  const issues: string[] = cov?.issues || []

  const aliasOf = (a: any) => {
    const raw = (a.alias || '').trim()
    if (raw && !raw.startsWith('本机')) return raw
    const mn = (a.machine_name || '').trim()
    if (mn) {
      const at = (a.agent_type || '').trim()
      return at && !mn.includes(at) ? `${mn} · ${at}` : mn
    }
    return raw || a.agent_id
  }

  if (real.length === 0) return null

  return (
    <div className="card card-pad" style={{ borderColor: degraded ? 'var(--block)' : 'var(--line)', background: degraded ? 'rgba(217,48,37,.03)' : undefined }}>
      <div className="row-between" style={{ marginBottom: 10, flexWrap: 'wrap', gap: 8 }}>
        <div className="row" style={{ gap: 8, alignItems: 'center' }}>
          <span className="h-sec h-sec-accent" style={{ fontSize: 12.5 }}>防护总览</span>
          <span className="small dim">{real.length} 个已接入智能体</span>
          {degraded && issues.slice(0, 3).map((iss: string, i: number) => (
            <span key={i} className="badge badge-block">{iss}</span>
          ))}
        </div>
        <div className="row" style={{ gap: 10, alignItems: 'center', minWidth: 200, flex: 1, maxWidth: 340 }}>
          <span className="small dim" style={{ whiteSpace: 'nowrap' }}>防护覆盖率</span>
          <div style={{ flex: 1, height: 8, background: 'var(--bg-2)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{ width: `${pct}%`, height: '100%', background: degraded ? 'var(--block)' : 'var(--allow)', borderRadius: 4, transition: 'width .4s' }} />
          </div>
          <span className="small" style={{ fontWeight: 700, color: degraded ? 'var(--block)' : 'var(--allow)', width: 36, textAlign: 'right' }}>{pct}%</span>
        </div>
      </div>

      {/* 每个 agent 一行: 平等展示, 不突出任何单个 */}
      <div className="col" style={{ gap: 6 }}>
        {real.map((a) => (
          <div key={a.agent_id} className="row" style={{ gap: 12, alignItems: 'center', padding: '8px 12px', borderRadius: 10, background: 'var(--bg-1)', border: '1px solid var(--line)' }}>
            <div style={{ width: 32, height: 32, borderRadius: 9, overflow: 'hidden', flexShrink: 0, display: 'grid', placeItems: 'center' }}>
              {logoFor(a.agent_type) ? <BrandLogo name={a.agent_type} size={22} /> : <span style={{ fontWeight: 800, fontSize: 14, color: 'var(--brand)' }}>{aliasOf(a).slice(0, 1).toUpperCase()}</span>}
            </div>
            <div style={{ minWidth: 0, flex: '0 1 220px' }}>
              <div style={{ fontWeight: 700, fontSize: 13.5, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{aliasOf(a)}</div>
              <div className="small mono dim" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.agent_id}</div>
            </div>

            <AgentModeSeg agentId={a.agent_id} invalidate={() => {
              qc.invalidateQueries({ queryKey: ['agent-mode'] })
              qc.invalidateQueries({ queryKey: ['coverage'] })
            }} />

            <span className="small" style={{ color: 'var(--brand)', cursor: 'pointer', marginLeft: 'auto', fontWeight: 600, whiteSpace: 'nowrap' }}
              onClick={() => nav(`/fleet/${encodeURIComponent(a.agent_id)}`)}>
              详情与管控 →
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

/** 单 agent 模式切换: 正常/限制/停用, 停用需二次确认 */
function AgentModeSeg({ agentId, invalidate }: { agentId: string; invalidate: () => void }) {
  const qc = useQueryClient()
  const [confirmKill, setConfirmKill] = useState(false)
  const { data: modeData } = useQuery({ queryKey: ['agent-mode', agentId], queryFn: () => api.agentMode(agentId), refetchInterval: 5000 })
  const mode = modeData?.mode || 'normal'

  const setMode = useMutation({
    mutationFn: (m: string) => api.setAgentMode(agentId, m),
    onSuccess: () => {
      setConfirmKill(false)
      invalidate()
      qc.invalidateQueries({ queryKey: ['agent-mode', agentId] })
    },
  })

  const segColor = (m: string) => (mode === m ? { background: m === 'kill' ? 'var(--block)' : m === 'quarantine' ? 'var(--warn)' : 'var(--allow)', color: '#fff', borderColor: 'transparent' } : undefined)

  return (
    <div className="row" style={{ gap: 6, alignItems: 'center', flexShrink: 0 }}>
      <div className="seg" style={{ flexShrink: 0 }}>
        <button className={`seg-item ${mode === 'normal' ? 'on' : ''}`} style={segColor('normal')} onClick={() => mode !== 'normal' && setMode.mutate('normal')}>● 正常</button>
        <button className={`seg-item ${mode === 'quarantine' ? 'on' : ''}`} style={segColor('quarantine')} onClick={() => mode !== 'quarantine' && setMode.mutate('quarantine')}>◐ 限制</button>
        <button className={`seg-item ${mode === 'kill' || confirmKill ? 'on' : ''}`} style={segColor('kill')}
          onClick={() => { if (confirmKill) setMode.mutate('kill'); else setConfirmKill(true) }}
          onMouseLeave={() => setConfirmKill(false)}>
          {confirmKill ? '确认停用?' : '✕ 停用'}
        </button>
      </div>
    </div>
  )
}

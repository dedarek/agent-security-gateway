import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { Agent } from '../lib/types'
import { StatusDot } from '../components/StatusDot'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'
import { HealthBar } from '../components/HealthBar'
import { Skeleton } from '../components/Skeleton'

/** Live — Agent workbench. Left: real agents list. Right: selected agent
 * detail (what it's doing, verdicts, chain timeline, sessions). Agent is the
 * first-class unit; charts live under the detail. */
export default function Live() {
  const { data: agents, isLoading } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 8000 })
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const list: Agent[] = agents || []
  const real = list.filter(isRealAgent)
  const testResidue = list.filter((a) => !isRealAgent(a))
  const [showTest, setShowTest] = useState(false)

  // auto-select first real agent
  const sel = selectedId ?? real[0]?.agent_id ?? null

  const kg = status?.kg || {}
  const active = real.filter((a) => a.status === 'active').length
  const online = real.filter((a) => a.status !== 'offline').length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      {/* top strip */}
      <div className="row-between" style={{ padding: '16px 22px 12px', borderBottom: '1px solid var(--line)' }}>
        <div>
          <h1 className="h-page">Agent 工作台</h1>
          <div className="small dim">以接入的 Agent 为基本单位 — 点选一个查看它在做什么</div>
        </div>
        <div className="row" style={{ gap: 14 }}>
          <Stat label="在线" value={online} color="var(--allow)" />
          <Stat label="活跃" value={active} color="var(--brand)" />
          <Stat label="KG 节点" value={kg.node_count ?? kg.entities ?? 0} />
          <span className="badge badge-allow">SSE LIVE</span>
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '320px 1fr', flex: 1, minHeight: 0 }}>
        {/* left: agent list */}
        <div style={{ borderRight: '1px solid var(--line)', overflowY: 'auto', background: 'var(--bg-1)' }}>
          <div className="card-pad" style={{ paddingBottom: 8 }}>
            <div className="h-sec">已接入 Agent <span className="dim">({real.length})</span></div>
          </div>
          {isLoading && <SkeletonRows n={4} h={52} />}
          {!isLoading && real.length === 0 && (
            <EmptyState icon="◇" title="暂无真 agent" hint="按 docs/ONBOARDING.md 一行接入；接入后此处出现。" />
          )}
          {real.map((a) => (
            <AgentRow key={a.agent_id} a={a} selected={sel === a.agent_id} onClick={() => setSelectedId(a.agent_id)} />
          ))}
          {testResidue.length > 0 && (
            <div style={{ borderTop: '1px solid var(--line)', marginTop: 8 }}>
              <button className="btn btn-ghost small" style={{ margin: '8px 14px', color: 'var(--fg-2)' }} onClick={() => setShowTest(!showTest)}>
                {showTest ? '▾' : '▸'} 测试/历史残留 ({testResidue.length})
              </button>
              {showTest && testResidue.map((a) => (
                <AgentRow key={a.agent_id} a={a} selected={sel === a.agent_id} onClick={() => setSelectedId(a.agent_id)} dimmed />
              ))}
            </div>
          )}
        </div>

        {/* right: selected detail */}
        <div style={{ overflowY: 'auto', minWidth: 0 }}>
          {sel ? <AgentWorkbench agentId={sel} /> : (
            <EmptyState icon="←" title="选择一个 Agent" hint="左侧列表点选一个已接入的 agent，这里显示它此刻的活动、裁决和链路。" />
          )}
        </div>
      </div>

      <div style={{ padding: '0 22px 16px' }}>
        <HealthBar />
      </div>
    </div>
  )
}

function Stat({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <span className="row" style={{ gap: 6 }}>
      <span style={{ fontSize: 20, fontWeight: 700, color: color || 'var(--fg-0)', fontVariantNumeric: 'tabular-nums' }}>{value}</span>
      <span className="small dim">{label}</span>
    </span>
  )
}

function AgentRow({ a, selected, onClick, dimmed }: { a: Agent; selected: boolean; onClick: () => void; dimmed?: boolean }) {
  return (
    <button onClick={onClick} style={{
      width: '100%', textAlign: 'left', background: selected ? 'var(--bg-2)' : 'transparent',
      border: 'none', borderLeft: `3px solid ${selected ? 'var(--brand)' : 'transparent'}`,
      padding: '11px 14px', cursor: 'pointer', color: 'inherit', font: 'inherit',
      opacity: dimmed ? 0.55 : 1, transition: 'background 120ms var(--ease)',
    }}>
      <div className="row" style={{ gap: 8 }}>
        <StatusDot status={a.status} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontWeight: 600, fontSize: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.alias || a.agent_id}</div>
          <div className="small dim mono" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.agent_id}</div>
        </div>
      </div>
      <div className="row small dim" style={{ gap: 6, marginTop: 3, flexWrap: 'wrap' }}>
        {a.agent_type && <span className="chip">{a.agent_type}</span>}
        {a.model && <span className="chip">{a.model}</span>}
        <span>{a.session_count ?? a.session_ids?.length ?? 0} 会话</span>
      </div>
    </button>
  )
}

function isRealAgent(a: Agent): boolean {
  const id = a.agent_id
  const testPrefix = /^(bugb-|final-|hook-agent|sectest-|e2e-|test-|audit-|rtt-|lv-|lineage-|tp\d|dbg-|rep-|g3-|gg-|fp\d|vchain|gfinal|clean-|chain-|eng\d|guard-|m3-|sess-|red-|probe-)/
  if (id === 'x') return false
  if (testPrefix.test(id)) return false
  return Boolean((a as any).machine_name || (a as any).machine_id)
}

function AgentWorkbench({ agentId }: { agentId: string }) {
  const { data, isLoading } = useQuery({ queryKey: ['agent-detail', agentId], queryFn: () => api.agentDetail(agentId), refetchInterval: 4000 })
  if (isLoading) return <div style={{ padding: 22 }}><Skeleton h={300} /></div>
  if (!data) return <EmptyState icon="✕" title="加载失败" />
  const a = data.agent
  const chain = data.chain || []
  const sessions = data.sessions || []
  const blocked = chain.filter((s: any) => s.verdict === 'BLOCK').length
  const confirms = chain.filter((s: any) => s.verdict === 'CONFIRM').length

  return (
    <div style={{ padding: 22, maxWidth: 1000 }}>
      <div className="row" style={{ gap: 10, marginBottom: 4 }}>
        <h2 style={{ margin: 0, fontSize: 17, fontWeight: 700 }}>{a.alias || a.agent_id}</h2>
        <StatusDot status={a.status} />
        <span className="small muted">{a.status}</span>
        {a.observed_model
          ? <span className="badge badge-allow">gateway-observed</span>
          : <span className="badge" style={{ color: 'var(--fg-2)', borderColor: 'var(--line)' }}>self-reported</span>}
      </div>
      <div className="small dim mono" style={{ marginBottom: 14 }}>
        {a.agent_id} · {a.agent_type || 'unknown'} · {a.model || '无模型'} {a.provider ? `(${a.provider})` : ''} · {a.ip || '-'}
      </div>

      <div className="row" style={{ gap: 12, marginBottom: 16 }}>
        <MiniStat label="拦截" value={blocked} color="var(--block)" />
        <MiniStat label="待确认" value={confirms} color="var(--confirm)" />
        <MiniStat label="会话" value={sessions.length} />
        <MiniStat label="链路步数" value={chain.length} />
      </div>

      <h3 className="h-sec" style={{ marginBottom: 8 }}>此刻在做什么 <span className="dim">(工作链路)</span></h3>
      {chain.length === 0 ? (
        <div className="card"><EmptyState icon="◌" title="暂无链路" hint="等待 hook 上报工具调用。" /></div>
      ) : (
        <div className="card card-pad">
          <div className="timeline">
            {chain.slice().reverse().slice(0, 20).map((s: any, i: number) => (
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

      <h3 className="h-sec" style={{ margin: '16px 0 8px' }}>会话</h3>
      <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
        {sessions.map((s: any) => (
          <a key={s.session_id} href={`/insight?tab=graph&focus=${encodeURIComponent(s.session_id)}`} className="chip" style={{ textDecoration: 'none' }}>
            {s.session_id} <span className="dim">({s.event_count})</span>
          </a>
        ))}
        {sessions.length === 0 && <span className="dim small">暂无会话</span>}
      </div>
    </div>
  )
}

function MiniStat({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div className="card" style={{ padding: '10px 16px', minWidth: 90 }}>
      <div style={{ fontSize: 20, fontWeight: 700, color: color || 'var(--fg-0)', fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      <div className="small dim">{label}</div>
    </div>
  )
}

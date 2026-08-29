import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useEventStream, type StreamStep } from '../lib/sse'
import type { Agent } from '../lib/types'
import { KPIBar } from '../components/KPIBar'
import { EventStream } from '../components/EventStream'
import { AgentCard } from '../components/AgentCard'
import { HealthBar } from '../components/HealthBar'
import { Drawer } from '../components/Drawer'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'

export default function Live() {
  const nav = useNavigate()
  const { data: agents } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 10000 })
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const { data: events } = useQuery({ queryKey: ['events'], queryFn: api.events, refetchInterval: 8000 })

  const [steps, setSteps] = useState<StreamStep[]>([])
  const [selected, setSelected] = useState<StreamStep | null>(null)

  const streamStatus = useEventStream((step) => {
    setSteps((prev) => [step, ...prev].slice(0, 200))
  })

  const list: Agent[] = agents || []
  const active = list.filter((a) => a.status === 'active')
  const idle = list.filter((a) => a.status === 'idle')

  // Seed the feed from the last 100 events when SSE hasn't pushed yet
  const feed: StreamStep[] = useMemo(() => {
    if (steps.length > 0) return steps
    return (events || []).slice(0, 60).map((e: any) => ({
      at: e?.Call?.Timestamp || e?.Timestamp || '',
      agent_id: e?.Call?.Principal?.AgentID || e?.session_id || '-',
      session_id: e?.Call?.Principal?.SessionID || e?.session_id || '',
      kind: 'tool_use',
      tool_name: e?.Call?.ToolID || '?',
      summary: safeSummary(e),
      verdict: e?.Decision?.Final != null ? String(verdictName(e.Decision.Final)) : 'ALLOW',
      reason: e?.Decision?.Rationale,
    }))
  }, [steps, events])

  const blocks = feed.filter((s) => s.verdict === 'BLOCK').length
  const confirms = feed.filter((s) => s.verdict === 'CONFIRM').length
  const kg = status?.kg || {}
  // "活跃" counts active only; "在线" counts active+idle (process alive).
  const online = active.length + idle.length

  return (
    <div className="col" style={{ padding: 22, gap: 16 }}>
      <div className="row-between">
        <div>
          <h1 className="h-page">实时台</h1>
          <div className="small dim">管控第一 · 拦截与确认在此一屏定生死</div>
        </div>
        <span className={`badge ${streamStatus === 'live' ? 'badge-allow' : 'badge-block'}`}>
          {streamStatus === 'live' ? 'SSE LIVE' : 'SSE DOWN'}
        </span>
      </div>

      <KPIBar items={[
        { label: '拦截 BLOCK', value: blocks, color: 'var(--block)' },
        { label: '待确认 CONFIRM', value: confirms, color: 'var(--confirm)' },
        { label: '活跃 Agent', value: active.length, color: 'var(--allow)' },
        { label: '在线 Agent', value: online },
        { label: 'KG 节点', value: kg.node_count ?? kg.entities ?? 0 },
      ]} />

      <div style={{ display: 'grid', gridTemplateColumns: '1.5fr 1fr', gap: 16, alignItems: 'start' }}>
        <EventStream steps={feed} live={streamStatus === 'live'} onSelect={setSelected} />
        <div className="col" style={{ gap: 12 }}>
          <div className="h-sec">在线 Agent</div>
          {online === 0 && (
            <EmptyState icon="◇" title="暂无在线 Agent" hint="按 docs/ONBOARDING.md 一行接入；注册后此处实时出现。" />
          )}
          {[...active, ...idle].slice(0, 6).map((a) => <AgentCard key={a.agent_id} a={a} />)}
        </div>
      </div>

      <HealthBar />

      <Drawer open={!!selected} onClose={() => setSelected(null)}
        title={<span className="row" style={{ gap: 8 }}><VerdictBadge v={selected?.verdict} /> {selected?.tool_name}</span>}>
        {selected && (
          <div className="col" style={{ gap: 14 }}>
            <dl className="kv">
              <dt>时间</dt><dd className="mono">{selected.at ? new Date(selected.at).toLocaleString('zh-CN') : '-'}</dd>
              <dt>Agent</dt><dd className="mono">{selected.agent_id}</dd>
              <dt>Session</dt><dd className="mono">{selected.session_id || '-'}</dd>
              <dt>工具</dt><dd>{selected.tool_name}</dd>
              <dt>裁决</dt><dd><VerdictBadge v={selected.verdict} /></dd>
            </dl>
            {selected.reason && (
              <div className="card card-pad" style={{ borderColor: 'rgba(255,95,86,.35)', background: 'rgba(255,95,86,.06)' }}>
                <div className="h-sec" style={{ marginBottom: 6 }}>拦截原因</div>
                <div className="small" style={{ wordBreak: 'break-all' }}>{selected.reason}</div>
              </div>
            )}
            <div className="card card-pad">
              <div className="h-sec" style={{ marginBottom: 6 }}>参数摘要</div>
              <div className="small mono" style={{ wordBreak: 'break-all' }}>{selected.summary || '-'}</div>
            </div>
            <button className="btn btn-primary" onClick={() => nav(`/insight?tab=graph&focus=${encodeURIComponent(selected.session_id)}`)}>
              在图谱中追溯 →
            </button>
          </div>
        )}
      </Drawer>
    </div>
  )
}

function verdictName(v: number | string): string {
  if (typeof v === 'string') return v
  return ['ALLOW', 'LOG', 'ALERT', 'CONFIRM', 'BLOCK', 'REDACT'][v] || 'ALLOW'
}

function safeSummary(e: any): string {
  const d = e?.Decision?.Rationale
  if (d) return String(d).slice(0, 160)
  const args = e?.Call?.Arguments
  if (!args) return ''
  try {
    const raw = typeof args === 'string' ? atob(args) : JSON.stringify(args)
    return raw.slice(0, 160)
  } catch { return '' }
}

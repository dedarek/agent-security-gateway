import { useEffect, useMemo, useState } from 'react'
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
import { VerdictBreakdown } from '../components/VerdictBreakdown'
import { Donut } from '../components/charts/Donut'
import { HBars } from '../components/charts/HBars'
import { Gauge } from '../components/charts/Gauge'
import { Trend } from '../components/charts/Trend'
import { Spark } from '../components/charts/Spark'

type Filter = { kind: 'verdict' | 'tool'; value: string } | null

export default function Live() {
  const nav = useNavigate()
  const { data: agents } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 10000 })
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const { data: stats } = useQuery({ queryKey: ['stats'], queryFn: () => api.statsSummary(300), refetchInterval: 8000 })

  const [steps, setSteps] = useState<StreamStep[]>([])
  const [selected, setSelected] = useState<StreamStep | null>(null)
  const [filter, setFilter] = useState<Filter>(null)
  const [spark, setSpark] = useState<number[]>(() => Array(30).fill(0))

  const streamStatus = useEventStream((step) => {
    setSteps((prev) => [step, ...prev].slice(0, 200))
    // bump the current minute bucket on the sparkline
    setSpark((prev) => {
      const next = prev.slice()
      next[next.length - 1] = (next[next.length - 1] || 0) + 1
      return next
    })
  })

  // roll the sparkline forward each minute
  useEffect(() => {
    const iv = setInterval(() => setSpark((p) => [...p.slice(1), 0]), 60000)
    return () => clearInterval(iv)
  }, [])

  const list: Agent[] = agents || []
  const active = list.filter((a) => a.status === 'active')
  const idle = list.filter((a) => a.status === 'idle')
  const online = active.length + idle.length

  const v = stats?.verdict || {}
  const blocks = v.block ?? 0
  const confirms = v.confirm ?? 0
  const allows = v.allow ?? 0
  const kg = status?.kg || {}

  // threat score: block heavy, confirm light, over the recent window
  const threat = Math.min(100, blocks * 10 + confirms * 2)

  // top risky agents from per_agent rollup
  const riskAgents: any[] = (stats?.per_agent || []).filter((a: any) => a.score > 0).slice(0, 5)
  const agentById = useMemo(() => {
    const m = new Map<string, Agent>()
    for (const a of (agents || [])) m.set(a.agent_id, a)
    return m
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify((agents || []).map((a: Agent) => a.agent_id + a.status))])

  const filteredSteps = useMemo(() => {
    if (!filter) return steps
    return steps.filter((s) => filter.kind === 'verdict' ? s.verdict === filter.value : s.tool_name === filter.value)
  }, [steps, filter])

  const onChartSelect = (kind: 'verdict' | 'tool') => (label: string) =>
    setFilter((f) => (f && f.kind === kind && f.value === label ? null : { kind, value: label }))

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
        { label: '放行 ALLOW', value: allows, color: 'var(--allow)' },
        { label: '在线 Agent', value: online },
        { label: 'KG 节点', value: kg.node_count ?? kg.entities ?? 0 },
      ]} />

      {/* 主作战网格：左构成 / 中事件流 / 右威胁 */}
      <div style={{ display: 'grid', gridTemplateColumns: '280px 1fr 290px', gap: 16, alignItems: 'start' }}>
        {/* 左列：构成分析 */}
        <div className="col" style={{ gap: 16 }}>
          <div className="card card-pad col" style={{ gap: 8, alignItems: 'center' }}>
            <div className="h-sec" style={{ alignSelf: 'flex-start' }}>裁决分布</div>
            <Donut
              centerLabel="近窗事件"
              slices={[
                { label: 'BLOCK', value: blocks, color: '#d93025' },
                { label: 'CONFIRM', value: confirms, color: '#e37400' },
                { label: 'ALLOW', value: allows, color: '#1e8e3e' },
              ]}
              onSelect={(l) => onChartSelect('verdict')(l)}
            />
            <div className="row small" style={{ gap: 10, justifyContent: 'center' }}>
              <span className="row" style={{ gap: 4 }}><i style={{ width: 8, height: 8, background: '#d93025', borderRadius: 2, display: 'inline-block' }} />拦截 {blocks}</span>
              <span className="row" style={{ gap: 4 }}><i style={{ width: 8, height: 8, background: '#e37400', borderRadius: 2, display: 'inline-block' }} />警告 {confirms}</span>
              <span className="row" style={{ gap: 4 }}><i style={{ width: 8, height: 8, background: '#1e8e3e', borderRadius: 2, display: 'inline-block' }} />放行 {allows}</span>
            </div>
          </div>

          <div className="card card-pad col" style={{ gap: 8 }}>
            <div className="h-sec">工具分布</div>
            <HBars
              items={(stats?.tools || []).slice(0, 6).map((t: any) => ({
                label: t.name, value: t.count,
                warn: t.name === 'Bash' || t.name === 'WebFetch',
                color: t.name === 'Bash' ? 'var(--confirm)' : 'var(--brand)',
              }))}
              onSelect={(l) => onChartSelect('tool')(l)}
            />
          </div>
        </div>

        {/* 中列：实时事件流（主角）+ 过滤提示 */}
        <div className="col" style={{ gap: 12, minWidth: 0 }}>
          {filter && (
            <div className="card card-pad row slide-in" style={{ gap: 10, borderColor: 'var(--brand)', background: 'rgba(26,115,232,.06)' }}>
              <span className="badge badge-redact">过滤中</span>
              <span className="small">{filter.kind === 'verdict' ? '裁决' : '工具'} = <b>{filter.value}</b> · {filteredSteps.length} 条</span>
              <button className="btn btn-ghost" style={{ marginLeft: 'auto', padding: '2px 8px' }} onClick={() => setFilter(null)}>× 清除</button>
            </div>
          )}
          <EventStream steps={filteredSteps} live={streamStatus === 'live'} onSelect={setSelected} />
        </div>

        {/* 右列：威胁优先级 */}
        <div className="col" style={{ gap: 16 }}>
          <div className="card card-pad col" style={{ gap: 4, alignItems: 'center' }}>
            <div className="h-sec" style={{ alignSelf: 'flex-start' }}>威胁等级</div>
            <Gauge value={threat} />
            <div className="small dim">近窗 {blocks} 拦截 · {confirms} 待确认</div>
          </div>

          <div className="card card-pad col" style={{ gap: 8 }}>
            <div className="h-sec">高风险 Agent</div>
            {riskAgents.length === 0 && <div className="small dim" style={{ padding: '6px 0' }}>当前无高风险 Agent</div>}
            {riskAgents.map((r: any) => {
              const a = agentById.get(r.agent_id)
              return (
                <button key={r.agent_id} onClick={() => nav(`/fleet/${encodeURIComponent(r.agent_id)}`)}
                  className="row-between" style={{ background: 'none', border: 'none', padding: '6px 0', cursor: 'pointer', borderBottom: '1px solid var(--line)', color: 'inherit', textAlign: 'left', width: '100%' }}>
                  <span className="row" style={{ gap: 8, minWidth: 0 }}>
                    <span className={`dot ${a ? (a.status === 'active' ? 'dot-active' : a.status === 'idle' ? 'dot-idle' : 'dot-offline') : 'dot-offline'}`} />
                    <span className="small" style={{ fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a?.alias || r.agent_id}</span>
                  </span>
                  <span className="row" style={{ gap: 6 }}>
                    {r.block > 0 && <span className="badge badge-block">{r.block} 拦</span>}
                    {r.confirm > 0 && <span className="badge badge-confirm">{r.confirm}</span>}
                  </span>
                </button>
              )
            })}
          </div>

          <div className="col" style={{ gap: 12 }}>
            <div className="h-sec">在线 Agent</div>
            {online === 0 && <EmptyState icon="◇" title="暂无在线 Agent" hint="按 docs/ONBOARDING.md 一行接入。" />}
            {[...active, ...idle].slice(0, 4).map((a) => <AgentCard key={a.agent_id} a={a} />)}
          </div>
        </div>
      </div>

      {/* 趋势 + 实时火花线 */}
      <div style={{ display: 'grid', gridTemplateColumns: '1.6fr 1fr', gap: 16 }}>
        <div className="card card-pad col" style={{ gap: 6 }}>
          <div className="h-sec">裁决趋势（近窗按小时）</div>
          <Trend data={stats?.by_hour || []} />
        </div>
        <div className="card card-pad col" style={{ gap: 6 }}>
          <div className="row-between">
            <div className="h-sec">实时活动</div>
            <span className="small dim mono">{spark.reduce((a, b) => a + b, 0)} 事件/30min</span>
          </div>
          <Spark points={spark} />
          <div className="small dim">SSE 每推一条，右端即长一点 — 流的活性证明</div>
        </div>
      </div>

      {/* 安全明细：BLOCK / 警告 / 放行 各自可下钻（SOC 基因） */}
      <div className="col" style={{ gap: 8 }}>
        <div className="h-sec">裁决明细 — 逐条看引擎为什么这么判</div>
        <VerdictBreakdown steps={steps} onTrace={(s) => nav(`/insight?tab=graph&focus=${encodeURIComponent(s.session_id)}`)} />
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
              <div className="card card-pad" style={{ borderColor: 'rgba(217,48,37,.35)', background: 'rgba(217,48,37,.06)' }}>
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

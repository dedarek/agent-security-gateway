import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { Agent } from '../lib/types'
import type { StreamStep } from '../lib/sse'
import { StatusDot } from '../components/StatusDot'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'
import { Skeleton } from '../components/Skeleton'
import { HealthBar } from '../components/HealthBar'
import { Drawer } from '../components/Drawer'
import { BrandChip, BrandLogo, logoFor } from '../assets/logos'
import { Donut } from '../components/charts/Donut'
import { Gauge } from '../components/charts/Gauge'
import { Trend } from '../components/charts/Trend'
import { EventStream } from '../components/EventStream'
import { CAPABILITY_GROUPS } from '../lib/capabilities'
import ProtectionStatus from '../components/ProtectionStatus'
import ApprovalQueue from '../components/ApprovalQueue'

const ACTIONS = ['allow', 'confirm', 'block'] as const

/** Live — GCP 中控大屏风。顶部 KPI 条 + 趋势/构成，下方 agent 大卡片网格。
 * 点卡弹抽屉（信息+管控+操作），再点「查看完整链路」才下钻日志明细。 */
export default function Live({ streamLive = true }: { streamLive?: boolean }) {
  const nav = useNavigate()
  const { data: agents, isLoading } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 8000 })
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const { data: stats } = useQuery({ queryKey: ['stats'], queryFn: () => api.statsSummary(300), refetchInterval: 8000 })
  const { data: streamSteps = [] } = useQuery<StreamStep[]>({ queryKey: ['stream-activity'], queryFn: async () => [], enabled: false, initialData: [] })
  const [drawerFor, setDrawerFor] = useState<string | null>(null)

  const [timeRange, setTimeRange] = useState<'24h' | '7d' | 'all'>('all')

  const list: Agent[] = agents || []
  const real = list.filter(isRealAgent)
  const kg = status?.kg || {}
  const v = stats?.verdict || {}
  const blocks = v.block ?? 0
  const confirms = v.confirm ?? 0
  const allows = v.allow ?? 0
  const online = real.filter((a) => a.status !== 'offline').length
  const active = real.filter((a) => a.status === 'active').length
  const onlineIds = new Set(real.filter((a) => a.status !== 'offline').map((a) => a.agent_id))
  const threat = Math.min(100, (stats?.per_agent || []).filter((p: any) => onlineIds.has(p.agent_id)).reduce((s: number, p: any) => s + p.block * 10 + p.confirm * 2, 0))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflowY: 'auto' }}>
      <div style={{ padding: '24px 28px 0' }}>
        <ProtectionStatus />
        <ApprovalQueue />
        <div className="row-between" style={{ marginBottom: 20, marginTop: 16 }}>
          <div>
            <h1 className="h-page" style={{ fontSize: 22, fontWeight: 600 }}>安全概览</h1>
          </div>
          <div className="row" style={{ gap: 12 }}>
            <div className="seg">
              <button className={`seg-item ${timeRange === '24h' ? 'on' : ''}`} onClick={() => setTimeRange('24h')}>近24小时</button>
              <button className={`seg-item ${timeRange === '7d' ? 'on' : ''}`} onClick={() => setTimeRange('7d')}>近7天</button>
              <button className={`seg-item ${timeRange === 'all' ? 'on' : ''}`} onClick={() => setTimeRange('all')}>全部</button>
            </div>
            <span className="badge badge-allow">实时连接中</span>
          </div>
        </div>

        {/* 顶部核心 KPI 卡片 */}
        <div className="row" style={{ gap: 16, flexWrap: 'wrap', marginBottom: 20 }}>
          <Kpi label="在线智能体" value={online} sub={`总注册: ${real.length} 个`} color="var(--brand)" icon="🤖" />
          <Kpi label="高危拦截" value={blocks} sub={`待确认: ${confirms} 件`} color="var(--block)" icon="🚫" />
          <Kpi label="正常放行" value={allows} sub={`活跃数: ${active}`} color="var(--allow)" icon="🛡️" />
          <Kpi label="本体安全节点" value={kg.node_count ?? kg.entities ?? 0} sub="KG 语义图谱" icon="🕸️" />
        </div>

        {/* 中间布局：Top5 排行 与 威胁分布 */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 20 }}>
          {/* Top 5 风险智能体 */}
          <div className="card card-pad col" style={{ gap: 12 }}>
            <div className="row-between">
              <div className="h-sec h-sec-accent" style={{ fontSize: 13 }}>Top 5 风险智能体</div>
              <span className="small dim">近窗事件数</span>
            </div>
            <div>
              {real.slice(0, 5).map((ag, idx) => {
                const p = (stats?.per_agent || []).find((x: any) => x.agent_id === ag.agent_id)
                const cnt = (p?.block || 0) * 2 + (p?.confirm || 0)
                const maxVal = Math.max(1, ...((stats?.per_agent || []).map((x: any) => (x.block || 0) * 2 + (x.confirm || 0))))
                const pct = Math.min(100, Math.round((cnt / maxVal) * 100))
                return (
                  <div key={ag.agent_id} className="rank-row">
                    <span className={`badge-rank ${idx === 0 ? 'badge-rank-1' : idx === 1 ? 'badge-rank-2' : idx === 2 ? 'badge-rank-3' : 'badge-rank-other'}`}>
                      {idx + 1}
                    </span>
                    <span style={{ width: 140, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {displayAlias(ag)}
                    </span>
                    <div className="rank-progress">
                      <div className="rank-progress-bar" style={{ width: `${Math.max(5, pct)}%`, background: idx === 0 ? 'var(--block)' : 'var(--brand)' }} />
                    </div>
                    <span style={{ width: 36, textAlign: 'right', fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>
                      {cnt}
                    </span>
                  </div>
                )
              })}
              {real.length === 0 && <div className="small dim" style={{ padding: '20px 0', textAlign: 'center' }}>暂无风险数据</div>}
            </div>
          </div>

          {/* Top 5 风险类型分布 */}
          <div className="card card-pad col" style={{ gap: 12 }}>
            <div className="row-between">
              <div className="h-sec h-sec-accent" style={{ fontSize: 13 }}>Top 5 风险事件类型</div>
              <span className="small dim">风险告警分布 · 实时</span>
            </div>
            <div>
              {((stats?.risks || []) as any[]).slice(0, 5).map((item, idx) => {
                const maxVal = Math.max(1, ...((stats?.risks || []).map((x: any) => x.count)))
                const pct = Math.min(100, Math.round((item.count / maxVal) * 100))
                return (
                  <div key={item.name} className="rank-row">
                    <span className={`badge-rank ${idx === 0 ? 'badge-rank-1' : idx === 1 ? 'badge-rank-2' : idx === 2 ? 'badge-rank-3' : 'badge-rank-other'}`}>
                      {idx + 1}
                    </span>
                    <span style={{ width: 190, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={item.name}>
                      {item.name}
                    </span>
                    <div className="rank-progress">
                      <div className="rank-progress-bar" style={{ width: `${Math.max(5, pct)}%`, background: idx === 0 ? 'var(--block)' : 'var(--confirm)' }} />
                    </div>
                    <span style={{ width: 36, textAlign: 'right', fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>
                      {item.count}
                    </span>
                  </div>
                )
              })}
              {(!stats?.risks || stats.risks.length === 0) && <div className="small dim" style={{ padding: '20px 0', textAlign: 'center' }}>暂无风险告警</div>}
            </div>
          </div>
        </div>

        {/* 底部全宽趋势图 */}
        <div className="card card-pad card-accent col" style={{ gap: 10, marginBottom: 20 }}>
          <div className="row-between">
            <div className="h-sec h-sec-accent" style={{ fontSize: 13 }}>风险拦截趋势</div>
            <div className="row" style={{ gap: 16, fontSize: 12 }}>
              <span className="row" style={{ gap: 6 }}><span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--brand)' }} /> 会话请求</span>
              <span className="row" style={{ gap: 6 }}><span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--block)' }} /> 高危拦截</span>
            </div>
          </div>
          <Trend data={stats?.by_hour || []} height={220} />
        </div>

        <div className="row-between" style={{ marginBottom: 10 }}>
          <div className="h-sec">已接入 Agent <span className="dim">({real.length})</span></div>
        </div>
        <div style={{ marginBottom: 16 }}>
          <EventStream steps={streamSteps} live={streamLive} />
        </div>
        {isLoading && <div className="row" style={{ gap: 14 }}><Skeleton h={150} w={320} /><Skeleton h={150} w={320} /></div>}
        {!isLoading && real.length === 0 && (
          <div className="card"><EmptyState icon="◇" title="暂无已接入 Agent" hint="按 docs/ONBOARDING.md 一行接入，接入后卡片出现在这里。" /></div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(330px, 1fr))', gap: 14, paddingBottom: 16 }}>
          {real.map((a) => <AgentBigCard key={a.agent_id} a={a} onOpen={() => setDrawerFor(a.agent_id)} />)}
        </div>

        <HealthBar />
        <div style={{ height: 20 }} />
      </div>

      <AgentDrawer agentId={drawerFor} onClose={() => setDrawerFor(null)} onDeepDive={(id) => { setDrawerFor(null); nav(`/fleet/${encodeURIComponent(id)}`) }} />
    </div>
  )
}

function Kpi({ label, value, sub, color, icon }: { label: string; value: number | string; sub?: string; color?: string; icon?: string }) {
  return (
    <div className="card card-hover" style={{ padding: '18px 20px', flex: '1 1 200px', minWidth: 180, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      <div>
        <div style={{ fontSize: 13, color: 'var(--fg-1)', fontWeight: 500, marginBottom: 6 }}>{label}</div>
        <div style={{ fontSize: 32, fontWeight: 800, color: color || 'var(--fg-0)', fontVariantNumeric: 'tabular-nums', lineHeight: 1.05, letterSpacing: '-0.02em' }}>{value}</div>
        {sub && <div className="small" style={{ color: 'var(--fg-2)', marginTop: 6 }}>{sub}</div>}
      </div>
      <div style={{ fontSize: 26, opacity: 0.9, filter: 'saturate(1.1)' }}>{icon || '📈'}</div>
    </div>
  )
}

function AgentBigCard({ a, onOpen }: { a: Agent; onOpen: () => void }) {
  const observed = (a as any).observed_model
  const alias = displayAlias(a)
  const machine = (a as any).machine_name || ''
  return (
    <button onClick={onOpen} className="card card-hover" style={{ textAlign: 'left', cursor: 'pointer', color: 'inherit', font: 'inherit', padding: '16px 18px', border: '1px solid var(--line)', display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div className="row-between" style={{ alignItems: 'flex-start' }}>
        <div className="row" style={{ gap: 10, minWidth: 0 }}>
          <span style={{ width: 38, height: 38, borderRadius: 10, background: 'var(--brand)', color: '#fff', display: 'grid', placeItems: 'center', fontWeight: 800, fontSize: 16, flexShrink: 0 }}>
            {alias.slice(0, 1).toUpperCase()}
          </span>
          <div style={{ minWidth: 0 }}>
            <div style={{ fontWeight: 800, fontSize: 15, lineHeight: 1.2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{alias}</div>
            <div className="small mono" style={{ color: 'var(--fg-2)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{a.agent_id}</div>
            {machine && <div className="small mono" style={{ color: 'var(--fg-2)', fontSize: 11 }}>{machine}</div>}
          </div>
        </div>
        <span style={{ flexShrink: 0, paddingTop: 2 }}><StatusDot status={a.status} /></span>
      </div>
      <div className="row small" style={{ gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        {a.agent_type && (
          (() => {
            const isCustom = a.agent_type.toLowerCase() === 'custom'
            if (isCustom) return <span className="chip" title="通用接入（harness-agnostic, agent_type=custom）" style={{ background: 'var(--bg-2)', borderStyle: 'dashed', color: 'var(--fg-1)', fontWeight: 600 }}>通用 · custom</span>
            return logoFor(a.agent_type) ? <BrandChip name={a.agent_type} /> : <span className="chip" style={{ background: 'var(--brand)', color: '#fff', borderColor: 'var(--brand)', fontWeight: 600 }}>{a.agent_type}</span>
          })()
        )}
        {a.model ? (
          <span className="row" style={{ gap: 4, alignItems: 'center' }}><span className="small dim">模型</span>{logoFor(a.model) ? <BrandChip name={a.model} style={{ background: 'var(--bg-2)', borderColor: 'var(--line)' }} /> : <span className="chip" style={{ background: 'var(--bg-2)', borderColor: 'var(--line)', fontWeight: 600 }}>{a.model}</span>}</span>
        ) : null}
        {a.provider && a.provider !== a.model && (
          <span className="row" style={{ gap: 4, alignItems: 'center' }}><span className="small dim">厂商</span>{logoFor(a.provider) ? <BrandChip name={a.provider} style={{ color: 'var(--fg-2)', borderStyle: 'dashed' }} /> : <span className="chip" style={{ color: 'var(--fg-2)', borderStyle: 'dashed' }}>{a.provider}</span>}</span>
        )}
        {observed ? <span className="chip" style={{ color: 'var(--allow)', borderColor: 'var(--allow)', background: 'rgba(30,142,62,.06)' }}>observed</span> : null}
      </div>
      <div className="row-between small" style={{ color: 'var(--fg-2)', borderTop: '1px solid var(--line)', paddingTop: 8 }}>
        <span>{a.session_count ?? a.session_ids?.length ?? 0} 会话</span>
        <span className="mono">{a.ip || '-'}</span>
      </div>
      <div className="small" style={{ color: 'var(--brand)', fontWeight: 600 }}>查看详情与管控 →</div>
    </button>
  )
}

function displayAlias(a: Agent): string {
  const raw = (a.alias || '').trim()
  if (raw && !raw.startsWith('本机')) return raw
  const mn = ((a as any).machine_name || '').trim()
  if (mn) {
    const at = (a.agent_type || '').trim()
    return at && !mn.includes(at) ? `${mn} · ${at}` : mn
  }
  return raw || a.agent_id
}

function AgentDrawer({ agentId, onClose, onDeepDive }: { agentId: string | null; onClose: () => void; onDeepDive: (id: string) => void }) {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['agent-detail', agentId], queryFn: () => api.agentDetail(agentId!), enabled: !!agentId, refetchInterval: 4000 })
  const { data: policies } = useQuery({ queryKey: ['policies'], queryFn: () => api.policies(), enabled: !!agentId })
  const upsert = useMutation({
    mutationFn: (body: any) => api.upsertPolicy(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
  const [ctrlOpen, setCtrlOpen] = useState(false)
  const [aliasEdit, setAliasEdit] = useState<string | null>(null)
  const [aliasVal, setAliasVal] = useState('')
  const [deleteConfirm, setDeleteConfirm] = useState(false)
  const setAlias = useMutation({
    mutationFn: ({ id, alias }: { id: string; alias: string }) => api.setAlias(id, alias),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['agent-detail', agentId] }); qc.invalidateQueries({ queryKey: ['agents'] }); setAliasEdit(null) },
  })
  const delAgent = useMutation({
    mutationFn: (id: string) => api.deleteAgent(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['agents'] }); onClose() },
  })
  if (!agentId) return null
  const a = data?.agent
  const chain = data?.chain || []
  const recent = chain.slice().reverse().slice(0, 6)
  const pols: any[] = policies || []
  const actionFor = (rule: string): string => {
    const per = pols.find((p) => p.agent_id === agentId && p.rule_id === rule)
    if (per) return per.action
    const glob = pols.find((p) => !p.agent_id && p.rule_id === rule)
    if (glob) return glob.action
    return 'allow'
  }
  const setAction = (rule: string, action: string, selector: Record<string, string>) =>
    upsert.mutate({ agent_id: agentId, rule_id: rule, action, axis: 'permission', enabled: true, selector })

  return (
    <>
    <Drawer open={!!agentId} onClose={onClose} title={a ? displayAlias(a) : '加载中…'} width={440}>
      {isLoading && <Skeleton h={200} />}
      {a && (
        <div className="col" style={{ gap: 16 }}>
          <div className="row" style={{ gap: 10 }}>
            <StatusDot status={a.status} />
            {a.observed_model ? <span className="badge badge-allow">gateway-observed</span> : <span className="badge" style={{ color: 'var(--fg-2)', borderColor: 'var(--line)' }}>self-reported</span>}
          </div>

          <dl className="kv">
            <dt>Agent ID</dt><dd className="mono">{a.agent_id}</dd>
            <dt>自定义名称</dt><dd>
              {aliasEdit === a.agent_id ? (
                <span className="row" style={{ gap: 6 }}>
                  <input className="input" value={aliasVal} onChange={(e) => setAliasVal(e.target.value)} placeholder={displayAlias(a)} style={{ flex: 1 }} />
                  <button className="btn btn-primary" onClick={() => setAlias.mutate({ id: a.agent_id, alias: aliasVal.trim() })} disabled={!aliasVal.trim()}>保存</button>
                  <button className="btn btn-ghost" onClick={() => setAliasEdit(null)}>取消</button>
                </span>
              ) : (
                <span className="row" style={{ gap: 8 }}>{displayAlias(a)} <button className="btn btn-ghost" style={{ padding: '2px 8px', fontSize: 11 }} onClick={() => { setAliasEdit(a.agent_id); setAliasVal(displayAlias(a)) }}>重命名</button></span>
              )}
            </dd>
            <dt>类型</dt><dd>{a.agent_type || '-'}</dd>
            <dt>模型</dt><dd>{a.model || '-'} {a.provider ? `(${a.provider})` : ''}</dd>
            <dt>机器</dt><dd>{(a as any).machine_name || '-'}</dd>
            <dt>IP</dt><dd className="mono">{a.ip || '-'}</dd>
            <dt>会话</dt><dd>{a.session_count ?? a.session_ids?.length ?? 0}</dd>
            <dt>最后活动</dt><dd className="small">{a.last_activity ? new Date(a.last_activity).toLocaleString('zh-CN') : '-'}</dd>
          </dl>

          <div className="row" style={{ gap: 8 }}>
            <button className="btn" style={{ justifyContent: 'space-between', flex: 1 }} onClick={() => setCtrlOpen(true)}>
              <span>能力管控</span>
              <span className="dim">→</span>
            </button>
            {deleteConfirm ? (
              <span className="row" style={{ gap: 6 }}>
                <button className="btn btn-danger" onClick={() => delAgent.mutate(a.agent_id)}>确认删除</button>
                <button className="btn btn-ghost" onClick={() => setDeleteConfirm(false)}>取消</button>
              </span>
            ) : (
              <button className="btn btn-ghost" style={{ color: 'var(--block)' }} onClick={() => setDeleteConfirm(true)}>删除</button>
            )}
          </div>
          {deleteConfirm && <div className="small dim">删除后再次接入将作为新 Agent 出现（历史已清）；若期间再发消息，网关会自动重建该 ID。</div>}

          <div>
            <div className="h-sec" style={{ marginBottom: 8 }}>最近活动</div>
            {recent.length === 0 && <div className="small dim">暂无活动</div>}
            {recent.map((s: any, i: number) => (
              <div key={i} className="row" style={{ gap: 8, padding: '6px 0', borderBottom: '1px solid var(--line)' }}>
                <VerdictBadge v={s.verdict} />
                <span className="chip">{s.tool || s.kind}</span>
                <span className="small dim" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.summary || '-'}</span>
              </div>
            ))}
          </div>

          <div className="col" style={{ gap: 8 }}>
            <button className="btn btn-primary" onClick={() => onDeepDive(a.agent_id)}>查看完整链路与日志 →</button>
            <div className="small dim">完整链路在舰队详情页；含工具调用时间线与裁决证据。</div>
          </div>
        </div>
      )}
    </Drawer>
    <Drawer open={ctrlOpen} onClose={() => setCtrlOpen(false)} title={a ? `${displayAlias(a)} · 能力管控` : '能力管控'} width={440}>
        <div className="col" style={{ gap: 14 }}>
          <div className="small dim" style={{ marginBottom: 0 }}>允许 = 直接放行 · 确认 = 需人工确认 · 拦截 = 直接阻断</div>
          {CAPABILITY_GROUPS.map((group) => (
            <section key={group.id}>
              <div className="h-sec" style={{ marginBottom: 2 }}>{group.label}</div>
              <div className="small dim" style={{ marginBottom: 6 }}>{group.hint}</div>
              <div className="col" style={{ gap: 7 }}>
                {group.items.map((c) => {
                  const cur = actionFor(c.rule_id)
                  return (
                    <div key={c.rule_id} className="card" style={{ padding: '10px 12px' }}>
                      <div className="row-between" style={{ gap: 8 }}>
                        <div style={{ minWidth: 0 }}>
                          <div className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
                            <span style={{ fontWeight: 600, fontSize: 13 }}>{c.label}</span>
                            {c.l2 && <span className="badge badge-confirm" style={{ fontSize: 10 }}>L2 高危</span>}
                            <span className="chip mono small">{c.rule_id}</span>
                          </div>
                          <div className="small dim" style={{ marginTop: 2 }}>{c.desc}</div>
                        </div>
                        <div className="seg" style={{ flexShrink: 0 }}>
                          {ACTIONS.map((act) => (
                            <button key={act}
                              className={`seg-item ${cur === act ? 'on' : ''}`}
                              style={cur === act ? segOnColor(act) : undefined}
                              onClick={() => setAction(c.rule_id, act, c.selector)}>
                              {act === 'allow' ? '允许' : act === 'confirm' ? '确认' : '拦截'}
                            </button>
                          ))}
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            </section>
          ))}
          <div className="small dim">未配置的细分项不继承新规则的授权；最终仍由 gateway/Rampart 的内置风险模型裁决。</div>
        </div>
    </Drawer>
    </>
  )
}

function segOnColor(act: string): React.CSSProperties {
  if (act === 'block') return { background: 'var(--block)', color: '#fff' }
  if (act === 'confirm') return { background: 'var(--confirm)', color: '#fff' }
  return { background: 'var(--allow)', color: '#fff' }
}

function isRealAgent(a: Agent): boolean {
  const id = a.agent_id
  if (id === 'x' || id.startsWith('claude-code-') || id.includes('macdemacbook')) return false
  const testPrefix = /^(bugb-|final-|hook-agent|sectest-|e2e-|test-|audit-|rtt-|lv-|lineage-|tp\d|dbg-|rep-|g3-|gg-|fp\d|vchain|gfinal|clean-|chain-|eng\d|guard-|m3-|sess-|red-|probe-)/
  if (testPrefix.test(id)) return false
  return Boolean((a as any).machine_name || (a as any).machine_id)
}

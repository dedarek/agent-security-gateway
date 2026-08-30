import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { Agent } from '../lib/types'
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

const CAPS: { rule_id: string; label: string; desc: string; l2?: boolean }[] = [
  { rule_id: 'Bash', label: 'Shell 执行', desc: '跑任意 shell 命令（高危）', l2: true },
  { rule_id: 'WebFetch', label: '网络外发', desc: '向外部 URL 发请求（数据外泄风险）', l2: true },
  { rule_id: 'Write', label: '写文件', desc: '创建/覆盖文件' },
  { rule_id: 'Edit', label: '改文件', desc: '编辑已有文件' },
  { rule_id: 'Read', label: '读文件', desc: '读取本地文件（含敏感路径检测）' },
  { rule_id: 'WebSearch', label: '联网搜索', desc: '发起网络搜索' },
]
const ACTIONS = ['allow', 'confirm', 'block'] as const

/** Live — GCP 中控大屏风。顶部 KPI 条 + 趋势/构成，下方 agent 大卡片网格。
 * 点卡弹抽屉（信息+管控+操作），再点「查看完整链路」才下钻日志明细。 */
export default function Live() {
  const nav = useNavigate()
  const { data: agents, isLoading } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 8000 })
  const { data: status } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const { data: stats } = useQuery({ queryKey: ['stats'], queryFn: () => api.statsSummary(300), refetchInterval: 8000 })
  const [drawerFor, setDrawerFor] = useState<string | null>(null)

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
      <div style={{ padding: '20px 24px 0' }}>
        <div className="row-between" style={{ marginBottom: 16 }}>
          <div>
            <h1 className="h-page">实时台</h1>
          </div>
          <span className="badge badge-allow">SSE LIVE</span>
        </div>

        <div className="row" style={{ gap: 12, flexWrap: 'wrap', marginBottom: 16 }}>
          <Kpi label="拦截 BLOCK" value={blocks} color="var(--block)" />
          <Kpi label="待确认 CONFIRM" value={confirms} color="var(--confirm)" />
          <Kpi label="放行 ALLOW" value={allows} color="var(--allow)" />
          <Kpi label="在线 Agent" value={online} />
          <Kpi label="活跃" value={active} color="var(--brand)" />
          <Kpi label="KG 节点" value={kg.node_count ?? kg.entities ?? 0} />
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '240px 240px 1fr', gap: 14, marginBottom: 16 }}>
          <div className="card card-pad col" style={{ alignItems: 'center', gap: 6 }}>
            <div className="h-sec" style={{ alignSelf: 'flex-start' }}>裁决分布</div>
            <Donut centerLabel="近窗" size={150} slices={[
              { label: 'BLOCK', value: blocks, color: '#d93025' },
              { label: 'CONFIRM', value: confirms, color: '#e37400' },
              { label: 'ALLOW', value: allows, color: '#1e8e3e' },
            ]} />
          </div>
          <div className="card card-pad col" style={{ alignItems: 'center', gap: 4 }}>
            <div className="h-sec" style={{ alignSelf: 'flex-start' }}>威胁等级</div>
            <Gauge value={threat} />
            <div className="small dim">在线 agent 近窗 {blocks} 拦 · {confirms} 待确认</div>
          </div>
          <div className="card card-pad col" style={{ gap: 6 }}>
            <div className="h-sec">裁决趋势（按小时）</div>
            <Trend data={stats?.by_hour || []} height={140} />
          </div>
        </div>

        <div className="row-between" style={{ marginBottom: 10 }}>
          <div className="h-sec">已接入 Agent <span className="dim">({real.length})</span></div>
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

function Kpi({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div className="card" style={{ padding: '12px 18px', flex: '1 1 150px', minWidth: 140 }}>
      <div style={{ fontSize: 26, fontWeight: 800, color: color || 'var(--fg-0)', fontVariantNumeric: 'tabular-nums', lineHeight: 1.1 }}>{value}</div>
      <div className="small dim" style={{ marginTop: 2 }}>{label}</div>
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
      <div className="row small" style={{ gap: 6, flexWrap: 'wrap' }}>
        {a.agent_type && (
          logoFor(a.agent_type) ? <BrandChip name={a.agent_type} /> : <span className="chip" style={{ background: 'var(--brand)', color: '#fff', borderColor: 'var(--brand)', fontWeight: 600 }}>{a.agent_type}</span>
        )}
        {a.model ? (
          logoFor(a.model) ? <BrandChip name={a.model} style={{ background: 'var(--bg-2)', borderColor: 'var(--line)' }} /> : <span className="chip" style={{ background: 'var(--bg-2)', borderColor: 'var(--line)', fontWeight: 600 }}>{a.model}</span>
        ) : <span className="chip" style={{ color: 'var(--fg-2)', borderStyle: 'dashed', background: 'var(--bg-1)' }}>模型未上报</span>}
        {a.provider && a.provider !== a.model && (
          logoFor(a.provider) ? <BrandChip name={a.provider} style={{ color: 'var(--fg-2)', borderStyle: 'dashed' }} /> : <span className="chip" style={{ color: 'var(--fg-2)', borderStyle: 'dashed' }}>{a.provider}</span>
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
  const setAction = (rule: string, action: string) =>
    upsert.mutate({ agent_id: agentId, rule_id: rule, action, axis: 'permission', enabled: true })

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
            <dt>类型</dt><dd>{a.agent_type || '-'}</dd>
            <dt>模型</dt><dd>{a.model || '-'} {a.provider ? `(${a.provider})` : ''}</dd>
            <dt>机器</dt><dd>{(a as any).machine_name || '-'}</dd>
            <dt>IP</dt><dd className="mono">{a.ip || '-'}</dd>
            <dt>会话</dt><dd>{a.session_count ?? a.session_ids?.length ?? 0}</dd>
            <dt>最后活动</dt><dd className="small">{a.last_activity ? new Date(a.last_activity).toLocaleString('zh-CN') : '-'}</dd>
          </dl>

          <button className="btn" style={{ justifyContent: 'space-between' }} onClick={() => setCtrlOpen(true)}>
            <span>能力管控</span>
            <span className="dim">→</span>
          </button>

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
      <div className="col" style={{ gap: 8 }}>
        <div className="small dim" style={{ marginBottom: 4 }}>允许 = 直接放行 · 确认 = 需人工确认 · 拦截 = 直接阻断</div>
        {CAPS.map((c) => {
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
                      onClick={() => setAction(c.rule_id, act)}>
                      {act === 'allow' ? '允许' : act === 'confirm' ? '确认' : '拦截'}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          )
        })}
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

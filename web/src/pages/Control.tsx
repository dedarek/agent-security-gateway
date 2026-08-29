import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { Agent } from '../lib/types'
import { StatusDot } from '../components/StatusDot'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'

// 能力项：一个 agent 能做/不能做某类操作。rule_id 对应引擎的工具/动作。
const CAPS: { rule_id: string; label: string; desc: string; l2?: boolean }[] = [
  { rule_id: 'Bash', label: 'Shell 执行', desc: '跑任意 shell 命令（高危）', l2: true },
  { rule_id: 'WebFetch', label: '网络外发', desc: '向外部 URL 发请求（数据外泄风险）', l2: true },
  { rule_id: 'Write', label: '写文件', desc: '创建/覆盖文件' },
  { rule_id: 'Edit', label: '改文件', desc: '编辑已有文件' },
  { rule_id: 'Read', label: '读文件', desc: '读取本地文件（含敏感路径检测）' },
  { rule_id: 'WebSearch', label: '联网搜索', desc: '发起网络搜索' },
]
const ACTIONS = ['allow', 'confirm', 'block'] as const

/** Control — per-agent capability policy. Pick an agent, toggle what it may do. */
export default function Control() {
  const qc = useQueryClient()
  const { data: agents, isLoading: la } = useQuery({ queryKey: ['agents'], queryFn: api.agents })
  const { data: policies } = useQuery({ queryKey: ['policies'], queryFn: () => api.policies() })
  const [selId, setSelId] = useState<string | null>(null)

  const real = (agents || []).filter(isRealAgent)
  const sel = selId ?? real[0]?.agent_id ?? null

  const upsert = useMutation({
    mutationFn: (body: any) => api.upsertPolicy(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })

  // current action for (agent, rule): per-agent policy > global > default allow
  const pols: any[] = policies || []
  const actionFor = (rule: string): string => {
    const per = pols.find((p) => p.agent_id === sel && p.rule_id === rule)
    if (per) return per.action
    const glob = pols.find((p) => !p.agent_id && p.rule_id === rule)
    if (glob) return glob.action
    return 'allow'
  }
  const setAction = (rule: string, action: string) =>
    upsert.mutate({ agent_id: sel, rule_id: rule, action, axis: 'permission', enabled: true })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '16px 22px 12px', borderBottom: '1px solid var(--line)' }}>
        <h1 className="h-page">管控</h1>
        <div className="small dim">选一个 Agent，配置它能不能做某类操作。改动立即生效。</div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '300px 1fr', flex: 1, minHeight: 0 }}>
        {/* agent picker */}
        <div style={{ borderRight: '1px solid var(--line)', overflowY: 'auto', background: 'var(--bg-1)' }}>
          <div className="card-pad" style={{ paddingBottom: 8 }}><div className="h-sec">Agent <span className="dim">({real.length})</span></div></div>
          {la && <SkeletonRows n={3} h={48} />}
          {!la && real.length === 0 && <EmptyState icon="◇" title="暂无 agent" hint="先接入一个 agent。" />}
          {real.map((a) => (
            <button key={a.agent_id} onClick={() => setSelId(a.agent_id)} style={{
              width: '100%', textAlign: 'left', background: sel === a.agent_id ? 'var(--bg-2)' : 'transparent',
              border: 'none', borderLeft: `3px solid ${sel === a.agent_id ? 'var(--brand)' : 'transparent'}`,
              padding: '10px 14px', cursor: 'pointer', color: 'inherit', font: 'inherit',
            }}>
              <div className="row" style={{ gap: 8 }}>
                <StatusDot status={a.status} />
                <span style={{ fontWeight: 600, fontSize: 13 }}>{a.alias || a.agent_id}</span>
              </div>
              <div className="small dim mono" style={{ marginTop: 2 }}>{a.agent_id}</div>
            </button>
          ))}
        </div>

        {/* capability grid */}
        <div style={{ overflowY: 'auto', padding: 22, minWidth: 0 }}>
          {!sel ? <EmptyState icon="←" title="选一个 Agent" hint="左侧选择要配置的 agent。" /> : (
            <>
              <div className="row" style={{ gap: 10, marginBottom: 6 }}>
                <h2 style={{ margin: 0, fontSize: 16, fontWeight: 700 }}>{real.find((x) => x.agent_id === sel)?.alias || sel}</h2>
                <StatusDot status={real.find((x) => x.agent_id === sel)?.status || 'offline'} />
              </div>
              <div className="small dim" style={{ marginBottom: 16 }}>允许 = 直接放行 · 确认 = 需人工确认 · 拦截 = 直接阻断</div>

              <div className="col" style={{ gap: 10, maxWidth: 640 }}>
                {CAPS.map((c) => {
                  const cur = actionFor(c.rule_id)
                  return (
                    <div key={c.rule_id} className="card card-pad row-between">
                      <div>
                        <div className="row" style={{ gap: 8 }}>
                          <span style={{ fontWeight: 600 }}>{c.label}</span>
                          {c.l2 && <span className="badge badge-confirm">L2 高危</span>}
                          <span className="chip mono small">{c.rule_id}</span>
                        </div>
                        <div className="small dim" style={{ marginTop: 3 }}>{c.desc}</div>
                      </div>
                      <div className="seg">
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
                  )
                })}
              </div>
              <div className="small dim" style={{ marginTop: 14 }}>
                规则写入 <code>/api/policies</code>（agent={sel}）。未配的项走内置三级风险模型默认值。
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function segOnColor(act: string): React.CSSProperties {
  if (act === 'block') return { background: 'var(--block)', color: '#fff' }
  if (act === 'confirm') return { background: 'var(--confirm)', color: '#fff' }
  return { background: 'var(--allow)', color: '#fff' }
}

function isRealAgent(a: Agent): boolean {
  const testPrefix = /^(bugb-|final-|hook-agent|sectest-|e2e-|test-|audit-|rtt-|lv-|lineage-|tp\d|dbg-|rep-|g3-|gg-|fp\d|vchain|gfinal|clean-|chain-|eng\d|guard-|m3-|sess-|red-|probe-)/
  if (a.agent_id === 'x' || testPrefix.test(a.agent_id)) return false
  return Boolean((a as any).machine_name || (a as any).machine_id)
}

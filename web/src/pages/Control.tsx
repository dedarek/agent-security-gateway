import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'
import { SkeletonRows } from '../components/Skeleton'

const AXES = ['permission', 'behavior', 'data_network', 'egress']

export default function Control() {
  const qc = useQueryClient()
  const [filter, setFilter] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ agent_id: '', rule_id: '', action: 'block', axis: 'permission' })

  const { data: policies, isLoading } = useQuery({ queryKey: ['policies', filter], queryFn: () => api.policies(filter || undefined) })
  const { data: approvals } = useQuery({ queryKey: ['approvals'], queryFn: api.approvals, refetchInterval: 5000 })
  const { data: suggestions } = useQuery({ queryKey: ['suggestions'], queryFn: api.suggestions, refetchInterval: 10000 })

  const upsert = useMutation({ mutationFn: (body: any) => api.upsertPolicy(body), onSuccess: () => { qc.invalidateQueries({ queryKey: ['policies'] }); setShowForm(false) } })
  const del = useMutation({ mutationFn: (id: number) => api.deletePolicy(id), onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }) })
  const changeAction = useMutation({
    mutationFn: (p: any) => api.upsertPolicy({ agent_id: p.agent_id, rule_id: p.rule_id, axis: p.axis, action: p.action, enabled: p.enabled !== false }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })

  const pendApprovals = approvals || []
  const pendSuggs = (suggestions || []).filter((s: any) => !s.decided && !s.decision)

  return (
    <div style={{ padding: 22 }}>
      <h1 className="h-page">管控</h1>
      <div className="small dim" style={{ marginBottom: 14 }}>策略即裁决：per-agent 优先于 global；action 下拉即改即存。</div>

      {/* 待处理 */}
      <h2 className="h-sec" style={{ marginBottom: 8 }}>待处理 <span className="dim">({pendApprovals.length + pendSuggs.length})</span></h2>
      {pendApprovals.length + pendSuggs.length === 0 ? (
        <div className="card" style={{ marginBottom: 18 }}>
          <EmptyState icon="✓" title="无待办" hint="没有待批准的审批，也没有未裁决的策略建议。系统在按既定策略自动裁决。" />
        </div>
      ) : (
        <div className="col" style={{ gap: 8, marginBottom: 18 }}>
          {pendApprovals.map((x: any, i: number) => (
            <div key={`a${i}`} className="card card-pad row-between">
              <div className="row" style={{ gap: 10 }}>
                <span className="badge badge-confirm">审批</span>
                <span className="small">{x.title || x.summary || JSON.stringify(x).slice(0, 120)}</span>
              </div>
            </div>
          ))}
          {pendSuggs.map((x: any, i: number) => (
            <div key={`s${i}`} className="card card-pad row-between">
              <div className="row" style={{ gap: 10 }}>
                <span className="badge badge-redact">建议</span>
                <span className="small">{x.title || x.rule_id || JSON.stringify(x).slice(0, 120)}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 策略表 */}
      <div className="row-between" style={{ marginBottom: 8 }}>
        <h2 className="h-sec">策略 <span className="dim">({(policies || []).length})</span></h2>
        <div className="row" style={{ gap: 8 }}>
          <input className="input" placeholder="按 agent_id 过滤（留空看全局）" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <button className="btn btn-primary" onClick={() => setShowForm(!showForm)}>{showForm ? '收起' : '+ 新增策略'}</button>
        </div>
      </div>

      {showForm && (
        <div className="card card-pad col slide-in" style={{ gap: 10, marginBottom: 14 }}>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
            <input className="input" placeholder="agent_id（留空为全局）" value={form.agent_id} onChange={(e) => setForm({ ...form, agent_id: e.target.value })} />
            <input className="input" placeholder="rule_id（如 Bash 或 *）" value={form.rule_id} onChange={(e) => setForm({ ...form, rule_id: e.target.value })} />
            <select className="select" value={form.axis} onChange={(e) => setForm({ ...form, axis: e.target.value })}>
              {AXES.map((x) => <option key={x} value={x}>{x}</option>)}
            </select>
            <select className="select" value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })}>
              {['block', 'confirm', 'redact', 'alert', 'log'].map((x) => <option key={x} value={x}>{x}</option>)}
            </select>
            <button className="btn btn-primary" disabled={!form.rule_id} onClick={() => upsert.mutate({ agent_id: form.agent_id || null, rule_id: form.rule_id, action: form.action, axis: form.axis, enabled: true })}>保存</button>
          </div>
        </div>
      )}

      <div className="card" style={{ overflow: 'hidden' }}>
        {isLoading ? <SkeletonRows n={4} /> : (
          <table className="table">
            <thead><tr><th>Agent</th><th>Rule</th><th>Axis</th><th>Action</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {(policies || []).map((p: any) => (
                <tr key={p.id}>
                  <td>{p.agent_id ? <span className="mono small">{p.agent_id}</span> : <span className="dim">global</span>}</td>
                  <td className="mono small">{p.rule_id}</td>
                  <td className="small muted">{p.axis}</td>
                  <td>
                    <select className="select" style={{ padding: '4px 8px', fontSize: 11 }} value={p.action}
                      onChange={(e) => changeAction.mutate({ ...p, action: e.target.value })}>
                      {['block', 'confirm', 'redact', 'alert', 'log'].map((x) => <option key={x} value={x}>{x}</option>)}
                    </select>
                  </td>
                  <td><VerdictBadge v={p.action} /></td>
                  <td><button className="btn btn-ghost" style={{ color: 'var(--block)', padding: '3px 10px', fontSize: 11 }} onClick={() => del.mutate(p.id)}>删除</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {!isLoading && (policies || []).length === 0 && (
          <EmptyState icon="◇" title="暂无策略" hint="默认全部走内置三级风险模型（L0 只读 / L1 写 / L2 高危）。新增策略可覆盖默认值。" />
        )}
      </div>
    </div>
  )
}

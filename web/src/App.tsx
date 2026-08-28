import { useState } from 'react'
import { BrowserRouter, Routes, Route, Link, useParams, NavLink } from 'react-router-dom'
import { QueryClient, QueryClientProvider, useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from './lib/api'
import type { Agent } from './lib/types'
import KGGraph from './components/KGGraph'

const qc = new QueryClient()

function StatusDot({ status }: { status: string }) {
  const color = status === 'active' ? '#35c48d' : status === 'idle' ? '#e8a317' : '#55677c'
  return <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, display: 'inline-block', boxShadow: status === 'active' ? '0 0 6px rgba(53,196,141,.6)' : 'none' }} />
}

function VerdictBadge({ v }: { v: string }) {
  const c = v === 'BLOCK' ? '#e15a4a' : v === 'CONFIRM' ? '#e8a317' : v === 'REDACT' ? '#4a9bd4' : '#35c48d'
  return <span style={{ padding: '2px 6px', borderRadius: 4, fontSize: 10, fontWeight: 700, background: c + '18', color: c, border: `1px solid ${c}40` }}>{v || 'ALLOW'}</span>
}

function Dashboard() {
  const { data: agents, isLoading } = useQuery({ queryKey: ['agents'], queryFn: api.agents, refetchInterval: 5000 })
  const { data: statusRaw } = useQuery({ queryKey: ['status'], queryFn: api.status as any, refetchInterval: 10000 } as any)
  const status: any = statusRaw
  if (isLoading) return <div style={{ padding: 24 }}>Loading...</div>
  const list: Agent[] = agents || []
  const active = list.filter(a => a.status === 'active').length
  const idle = list.filter(a => a.status === 'idle').length
  return (
    <div style={{ padding: 24 }}>
      <h1 style={{ fontSize: 20, fontWeight: 700, marginBottom: 16 }}>Agent 概览</h1>
      <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
        <div style={{ padding: '12px 16px', background: '#161d26', borderRadius: 8, border: '1px solid #232d3b' }}>活跃 <b style={{ color: '#35c48d' }}>{active}</b> · 空闲 <b style={{ color: '#e8a317' }}>{idle}</b> · 离线 <b>{list.length - active - idle}</b></div>
        <div style={{ padding: '12px 16px', background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', fontSize: 11, color: '#8092a6' }}>KG: {status?.kg?.entities ?? '-'} entities · {status?.kg?.indexed ?? '-'} indexed</div>
      </div>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
        <thead><tr style={{ color: '#8092a6', borderBottom: '1px solid #232d3b' }}><th style={{ textAlign: 'left', padding: '8px' }}>Agent</th><th>状态</th><th>模型</th><th>会话</th><th>最后活动</th><th>操作</th></tr></thead>
        <tbody>
          {list.map(a => (
            <tr key={a.agent_id} style={{ borderBottom: '1px solid #1a2430' }}>
              <td style={{ padding: '10px 8px' }}><div style={{ fontWeight: 600 }}>{a.alias || a.agent_id}</div><div style={{ fontSize: 10, color: '#8092a6' }}>{a.agent_id} · {a.agent_type}</div></td>
              <td style={{ textAlign: 'center' }}><StatusDot status={a.status} /> <span style={{ fontSize: 11, marginLeft: 4 }}>{a.status}</span></td>
              <td style={{ textAlign: 'center', fontSize: 11 }}>{a.model || '-'} <span style={{ color: '#8092a6', fontSize: 10 }}>{a.provider ? `(${a.provider})` : ''}</span></td>
              <td style={{ textAlign: 'center' }}>{a.session_count ?? '-'}</td>
              <td style={{ textAlign: 'center', fontSize: 11, color: '#8092a6' }}>{a.last_activity || '-'}</td>
              <td style={{ textAlign: 'center' }}><Link to={`/agents/${encodeURIComponent(a.agent_id)}`} style={{ color: '#e8a317' }}>详情</Link></td>
            </tr>
          ))}
        </tbody>
      </table>
      {list.length === 0 && <div style={{ padding: 24, textAlign: 'center', color: '#8092a6' }}>暂无已注册 Agent — 按 ONBOARDING.md 接入</div>}
    </div>
  )
}

function AgentDetail() {
  const { id } = useParams()
  const agentId = decodeURIComponent(id || '')
  const { data, isLoading } = useQuery({ queryKey: ['agent-detail', agentId], queryFn: () => api.agentDetail(agentId), refetchInterval: 3000 })
  const { data: history } = useQuery({ queryKey: ['history', agentId], queryFn: () => api.agentHistory(agentId) })
  const qc = useQueryClient()
  const del = useMutation({
    mutationFn: () => api.deleteAgent(agentId),
    onSuccess: () => { alert('已删除'); window.location.href = '/' }
  })
  if (isLoading) return <div style={{ padding: 24 }}>Loading...</div>
  if (!data) return <div style={{ padding: 24 }}>Not found</div>
  const chain = data.chain || []
  const sessions = data.sessions || []
  return (
    <div style={{ padding: 24 }}>
      <Link to="/" style={{ color: '#8092a6', fontSize: 12 }}>← 返回</Link>
      <h1 style={{ fontSize: 18, fontWeight: 700, marginTop: 8 }}>{data.agent.alias || data.agent.agent_id} <StatusDot status={data.agent.status} /> <span style={{ fontSize: 12, color: '#8092a6' }}>{data.agent.status}</span></h1>
      <div style={{ fontSize: 11, color: '#8092a6', marginBottom: 12 }}>{data.agent.agent_id} · {data.agent.agent_type} · {data.agent.model} {data.agent.observed_model ? <span style={{ background: '#35c48d18', color: '#35c48d', padding: '1px 4px', borderRadius: 4, fontSize: 10 }}>gateway-observed</span> : <span style={{ background: '#8092a618', color: '#8092a6', padding: '1px 4px', borderRadius: 4, fontSize: 10 }}>self-reported</span>} · {data.agent.ip}</div>

      <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
        <button onClick={() => del.mutate()} style={{ padding: '6px 12px', background: '#e15a4a', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer' }}>删除 Agent（仅离线可删）</button>
        <button onClick={() => qc.invalidateQueries({ queryKey: ['agent-detail'] })} style={{ padding: '6px 12px', background: '#1a2430', color: '#e6ebf2', border: '1px solid #232d3b', borderRadius: 6 }}>刷新</button>
      </div>

      <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>工作链路 <span style={{ fontSize: 11, color: '#8092a6' }}>{chain.length} 步</span></h2>
      {chain.length === 0 ? <div style={{ color: '#8092a6', padding: 12, background: '#161d26', borderRadius: 8 }}>暂无链路 — 等待 hook 上报</div> : (
        <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', overflow: 'hidden' }}>
          {chain.map((s: any, i: number) => (
            <div key={i} style={{ display: 'flex', gap: 12, padding: '10px 14px', borderBottom: i === chain.length - 1 ? 'none' : '1px solid #1a2430', alignItems: 'center' }}>
              <span style={{ fontSize: 11, color: '#8092a6', minWidth: 130 }}>{s.at}</span>
              <span style={{ fontWeight: 600, minWidth: 80 }}>{s.tool || s.kind}</span>
              <span style={{ flex: 1, fontSize: 12, color: '#c9d3de', overflow: 'hidden', textOverflow: 'ellipsis' }}>{s.summary || '-'}</span>
              <VerdictBadge v={s.verdict} />
            </div>
          ))}
        </div>
      )}

      <h2 style={{ fontSize: 14, fontWeight: 600, margin: '16px 0 8px' }}>模型变更历史</h2>
      <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', padding: 12 }}>
        {history?.history?.length ? history.history.map((h: any, i: number) => (
          <div key={i} style={{ fontSize: 12, padding: '4px 0', borderBottom: '1px solid #1a2430' }}>{new Date(h.at).toLocaleString()} — {h.from || '(none)'} → <b>{h.to}</b> <span style={{ color: '#8092a6' }}>({h.source})</span></div>
        )) : <span style={{ color: '#8092a6' }}>无变更</span>}
      </div>

      <h2 style={{ fontSize: 14, fontWeight: 600, margin: '16px 0 8px' }}>会话</h2>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {sessions.map((s: any) => <span key={s.session_id} style={{ padding: '4px 8px', background: '#1a2430', borderRadius: 6, fontSize: 11 }}>{s.session_id} <span style={{ color: '#8092a6' }}>({s.event_count})</span></span>)}
        {sessions.length === 0 && <span style={{ color: '#8092a6' }}>暂无会话</span>}
      </div>
    </div>
  )
}

function Policies() {
  const [filter, setFilter] = useState('')
  const { data, refetch } = useQuery({ queryKey: ['policies', filter], queryFn: () => api.policies(filter || undefined) })
  const [form, setForm] = useState({ agent_id: '', rule_id: '', action: 'block', axis: 'permission' })
  const qc = useQueryClient()
  const upsert = useMutation({
    mutationFn: (body: any) => api.upsertPolicy(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] })
  })
  const del = useMutation({
    mutationFn: (id: number) => api.deletePolicy(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] })
  })
  return (
    <div style={{ padding: 24 }}>
      <h1 style={{ fontSize: 18, fontWeight: 700 }}>策略</h1>
      <p style={{ color: '#8092a6', fontSize: 12, marginBottom: 12 }}>Per-agent 策略：agent_specific 优先于 global，支持 log/alert/block/confirm</p>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input placeholder="按 agent_id 过滤（留空看全局）" value={filter} onChange={e => setFilter(e.target.value)} style={{ padding: '6px 10px', background: '#161d26', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2' }} />
        <button onClick={() => refetch()} style={{ padding: '6px 12px', background: '#1a2430', border: '1px solid #232d3b', borderRadius: 6 }}>刷新</button>
      </div>
      <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', overflow: 'hidden', marginBottom: 16 }}>
        <table style={{ width: '100%', fontSize: 12, borderCollapse: 'collapse' }}>
          <thead><tr style={{ color: '#8092a6', borderBottom: '1px solid #232d3b' }}><th style={{ padding: 8, textAlign: 'left' }}>Agent</th><th>Rule</th><th>Axis</th><th>Action</th><th>操作</th></tr></thead>
          <tbody>
            {(data || []).map((p: any) => (
              <tr key={p.id} style={{ borderBottom: '1px solid #1a2430' }}>
                <td style={{ padding: 8 }}>{p.agent_id || <span style={{ color: '#8092a6' }}>global</span>}</td>
                <td style={{ padding: 8 }}>{p.rule_id}</td>
                <td style={{ padding: 8 }}>{p.axis}</td>
                <td style={{ padding: 8 }}><VerdictBadge v={p.action.toUpperCase()} /></td>
                <td style={{ padding: 8 }}><button onClick={() => del.mutate(p.id)} style={{ color: '#e15a4a', background: 'none', border: 'none', cursor: 'pointer' }}>删除</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {(data || []).length === 0 && <div style={{ padding: 16, textAlign: 'center', color: '#8092a6' }}>暂无策略</div>}
      </div>
      <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', padding: 16 }}>
        <h3 style={{ fontWeight: 600, marginBottom: 8 }}>新增/更新策略</h3>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <input placeholder="agent_id（留空为全局）" value={form.agent_id} onChange={e => setForm({ ...form, agent_id: e.target.value })} style={{ padding: '6px 10px', background: '#0d1116', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2' }} />
          <input placeholder="rule_id（如 Bash 或 *）" value={form.rule_id} onChange={e => setForm({ ...form, rule_id: e.target.value })} style={{ padding: '6px 10px', background: '#0d1116', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2' }} />
          <select value={form.action} onChange={e => setForm({ ...form, action: e.target.value })} style={{ padding: '6px 10px', background: '#0d1116', border: '1px solid #232d3b', borderRadius: 6, color: '#e6ebf2' }}>
            <option value="block">block</option><option value="confirm">confirm</option><option value="redact">redact</option><option value="alert">alert</option><option value="log">log</option>
          </select>
          <button onClick={() => upsert.mutate({ agent_id: form.agent_id || null, rule_id: form.rule_id, action: form.action, axis: form.axis, enabled: true })} style={{ padding: '6px 14px', background: '#e8a317', color: '#0d1116', border: 'none', borderRadius: 6, fontWeight: 600, cursor: 'pointer' }}>保存</button>
        </div>
      </div>
    </div>
  )
}

function Graph() {
  return <KGGraph />
}

function Findings() {
  const { data: judge } = useQuery({ queryKey: ['judge'], queryFn: api.judgeFindings })
  const { data: monitor } = useQuery({ queryKey: ['monitor'], queryFn: api.monitorFindings })
  return (
    <div style={{ padding: 24 }}>
      <h1 style={{ fontSize: 18, fontWeight: 700 }}>发现</h1>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginTop: 12 }}>
        <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', padding: 16 }}>
          <h3 style={{ fontWeight: 600, marginBottom: 8 }}>Judge</h3>
          <pre style={{ fontSize: 11, color: '#8092a6', maxHeight: 300, overflow: 'auto' }}>{JSON.stringify(judge || [], null, 2).slice(0, 3000) || '暂无'}</pre>
        </div>
        <div style={{ background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', padding: 16 }}>
          <h3 style={{ fontWeight: 600, marginBottom: 8 }}>Monitor</h3>
          <pre style={{ fontSize: 11, color: '#8092a6', maxHeight: 300, overflow: 'auto' }}>{JSON.stringify(monitor || [], null, 2).slice(0, 3000) || '暂无'}</pre>
        </div>
      </div>
    </div>
  )
}

function Sessions() {
  const { data } = useQuery({ queryKey: ['sessions'], queryFn: api.sessions })
  return (
    <div style={{ padding: 24 }}>
      <h1 style={{ fontSize: 18, fontWeight: 700 }}>会话</h1>
      <div style={{ marginTop: 12, background: '#161d26', borderRadius: 8, border: '1px solid #232d3b', overflow: 'hidden' }}>
        <table style={{ width: '100%', fontSize: 12, borderCollapse: 'collapse' }}>
          <thead><tr style={{ color: '#8092a6', borderBottom: '1px solid #232d3b' }}><th style={{ padding: 8, textAlign: 'left' }}>Session</th><th>Events</th><th>Last Verdict</th></tr></thead>
          <tbody>
            {(data || []).map((s: any) => (
              <tr key={s.session_id} style={{ borderBottom: '1px solid #1a2430' }}><td style={{ padding: 8 }}>{s.session_id}</td><td style={{ textAlign: 'center' }}>{s.events}</td><td style={{ textAlign: 'center' }}><VerdictBadge v={s.last_verdict} /></td></tr>
            ))}
          </tbody>
        </table>
        {(!data || data.length === 0) && <div style={{ padding: 16, textAlign: 'center', color: '#8092a6' }}>暂无会话</div>}
      </div>
    </div>
  )
}

function Layout() {
  const nav = [
    { to: '/', label: '概览', end: true },
    { to: '/policies', label: '策略' },
    { to: '/graph', label: '图谱' },
    { to: '/findings', label: '发现' },
    { to: '/sessions', label: '会话' },
  ]
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '200px 1fr', height: '100vh', background: '#0d1116' }}>
      <div style={{ background: '#11181f', borderRight: '1px solid #232d3b', display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '16px 14px', borderBottom: '1px solid #232d3b' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontWeight: 700 }}><span style={{ width: 28, height: 28, borderRadius: 8, background: '#e8a317', display: 'grid', placeItems: 'center', color: '#0d1116' }}>ASG</span> Agent Gateway</div>
          <div style={{ fontSize: 10, color: '#8092a6', marginTop: 4 }}>SaaS · 管控第一</div>
        </div>
        <div style={{ flex: 1, padding: 10 }}>
          {nav.map(n => (
            <NavLink key={n.to} to={n.to} end={n.end} style={({ isActive }) => ({ display: 'block', padding: '8px 10px', borderRadius: 8, color: isActive ? '#e6ebf2' : '#8092a6', background: isActive ? '#1a2430' : 'transparent', textDecoration: 'none', fontSize: 13, fontWeight: 600, marginBottom: 2 })}>
              {n.label}
            </NavLink>
          ))}
        </div>
        <div style={{ padding: 12, borderTop: '1px solid #232d3b', fontSize: 11, color: '#8092a6' }}>ASG v1 · <a href="https://github.com/dedarek/agent-security-gateway" style={{ color: '#e8a317' }}>GitHub</a></div>
      </div>
      <div style={{ overflow: 'auto' }}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/agents/:id" element={<AgentDetail />} />
          <Route path="/policies" element={<Policies />} />
          <Route path="/graph" element={<Graph />} />
          <Route path="/findings" element={<Findings />} />
          <Route path="/sessions" element={<Sessions />} />
        </Routes>
      </div>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Layout />
      </BrowserRouter>
    </QueryClientProvider>
  )
}

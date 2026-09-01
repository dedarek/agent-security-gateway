const base = ''

async function get<T>(path: string): Promise<T> {
  const r = await fetch(base + path, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
async function post<T>(path: string, body: any): Promise<T> {
  const r = await fetch(base + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

async function put<T>(path: string, body: any): Promise<T> {
  const r = await fetch(base + path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

async function del<T>(path: string): Promise<T> {
  const r = await fetch(base + path, { method: 'DELETE', credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export const api = {
  agents: () => get<any[]>('/api/agents'),
  agentDetail: (id: string) => get<any>(`/api/agents/detail?agent_id=${encodeURIComponent(id)}`),
  agentHistory: (id: string) => get<any>(`/api/agents/history?agent_id=${encodeURIComponent(id)}`),
  deleteAgent: (id: string) => del(`/api/agents/delete?agent_id=${encodeURIComponent(id)}`),
  setAlias: (id: string, alias: string) => post(`/api/agents/action`, { agent_id: id, alias }),
  policies: (agentId?: string) => get<any[]>(`/api/policies${agentId ? `?agent_id=${agentId}` : '?all=true'}`),
  upsertPolicy: (body: any) => put('/api/policies', body),
  deletePolicy: (id: number) => del(`/api/policies?id=${id}`),
  events: () => get<any[]>('/api/events'),
  sessions: () => get<any[]>('/api/sessions'),
  kgNodes: () => get<any>('/api/kg/graph/nodes'),
  kgEdges: () => get<any>('/api/kg/graph/edges'),
  kgSearch: (q: string) => get<any>(`/api/kg/search?q=${encodeURIComponent(q)}`),
  judgeFindings: () => get<any>('/api/judge/findings'),
  monitorFindings: () => get<any>('/api/monitor/findings'),
  status: () => get<any>('/api/status'),
  statsSummary: (n = 300) => get<any>(`/api/stats/summary?n=${n}`),
  ontoStories: () => get<any>('/api/onto/stories'),
  ontoLineage: (focus?: string) => get<any>(`/api/onto/lineage${focus ? `?focus=${encodeURIComponent(focus)}` : ''}`),
  ontoEvidence: (eventId: string) => get<any>(`/api/onto/evidence?event=${encodeURIComponent(eventId)}`),
  ontoGraph: () => get<any>('/api/onto/graph'),
  approvals: () => get<any[]>('/api/approvals'),
  suggestions: () => get<any[]>('/api/suggestions'),
  trajectory: (session: string) => get<any>(`/api/trajectory?session=${encodeURIComponent(session)}`),
  kgPath: (source: string, target: string) => get<any>(`/api/kg/graph/path?source=${encodeURIComponent(source)}&target=${encodeURIComponent(target)}`),
  dataAccess: (agentId: string) => get<any>(`/api/data-access?agent_id=${encodeURIComponent(agentId)}`),
  dataAccessByTrace: (traceId: string) => get<any>(`/api/data-access?trace_id=${encodeURIComponent(traceId)}`),
  dataAccessRecent: () => get<any>('/api/data-access'),
  coverage: () => get<any>('/api/coverage'),
  agentMode: (id: string) => get<any>(`/api/agents/mode?agent_id=${encodeURIComponent(id)}`),
  setAgentMode: (id: string, mode: string) => post<any>('/api/agents/mode', { agent_id: id, mode }),
}

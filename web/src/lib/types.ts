export type Agent = {
  agent_id: string
  alias?: string
  agent_type?: string
  machine_name?: string
  model?: string
  provider?: string
  status: string
  isolation: string
  session_ids?: string[]
  session_count?: number
  last_activity?: string
  last_heartbeat?: string
  registered_at?: string
  ip?: string
}

export type ChainStep = {
  at: string
  agent_id: string
  session_id: string
  kind: string
  tool: string
  summary: string
  verdict: string
}

export type AgentDetail = {
  agent: Agent
  sessions: { session_id: string; event_count: number; last_activity: string }[]
  timeline: any[]
  chain: ChainStep[] | null
}

export type Policy = {
  id: number
  agent_id: string | null
  axis: string
  rule_id: string
  action: string
  enabled: boolean
  updated_at: number
}

export type HistoryEntry = {
  at: string
  field: string
  from: string
  to: string
  source: string
}

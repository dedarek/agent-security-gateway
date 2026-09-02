import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEventStream } from './lib/sse'
import Live from './pages/Live'
import DlpDemo from './pages/DlpDemo'
import AgentDetail from './pages/AgentDetail'
import Insight from './pages/Insight'
import Fleet from './pages/Fleet'
import Control from './pages/Control'
import SemanticaPage from './pages/SemanticaPage'
import { AsgMark } from './assets/AsgMark'

const qc = new QueryClient()

const NAV_GROUPS = [
  {
    title: '概览 & 洞察',
    items: [
      { to: '/', label: '安全概览', icon: '🛡️', end: true },
      { to: '/insight', label: '本体洞察', icon: '📊' },
      { to: '/semantica', label: '知识图谱', icon: '🕸️' },
    ]
  },
  {
    title: '资产',
    items: [
      { to: '/agents', label: '智能体', icon: '🤖' },
    ]
  },
  {
    title: '防护管控',
    items: [
      { to: '/policies', label: '高危操作拦截', icon: '🚫' },
      { to: '/dlp', label: '敏感信息保护', icon: '🔒' },
    ]
  }
]

function Shell() {
  const stream = useEventStream()
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '220px 1fr', height: '100vh', background: 'var(--bg-0)' }}>
      <aside className="sidebar" style={{ display: 'flex', flexDirection: 'column', borderRight: '1px solid rgba(255,255,255,.06)' }}>
        <div style={{ padding: '18px 18px 16px', borderBottom: '1px solid rgba(255,255,255,.08)' }}>
          <div className="row" style={{ gap: 11, alignItems: 'center' }}>
            <AsgMark size={30} />
            <div className="col" style={{ gap: 1 }}>
              <span style={{ fontWeight: 800, fontSize: 16, color: '#fff', letterSpacing: '0.01em', lineHeight: 1.1 }}>AgentSentry</span>
              <span style={{ fontSize: 10.5, color: '#8aa0c0', fontWeight: 600, letterSpacing: '0.14em', textTransform: 'uppercase' }}>ASG · 智能体安全网关</span>
            </div>
          </div>
        </div>
        <nav style={{ flex: 1, padding: '12px 10px', overflowY: 'auto' }}>
          {NAV_GROUPS.map((grp, gIdx) => (
            <div key={gIdx} style={{ marginBottom: 16 }}>
              <div style={{ padding: '4px 10px 6px', fontSize: 11, color: '#86909c', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                {grp.title}
              </div>
              {grp.items.map((n) => (
                <NavLink key={n.to} to={n.to} end={n.end}
                  className={({ isActive }) => `navlink ${isActive ? 'active' : ''}`}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', fontSize: 13, borderRadius: 6 }}>
                  <span style={{ fontSize: 14 }}>{n.icon}</span>
                  <span>{n.label}</span>
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div style={{ padding: '12px 16px', borderTop: '1px solid rgba(255,255,255,.08)', fontSize: 11, color: '#86909c' }}>
          <div className="row" style={{ gap: 6 }}>
            <span className={`health-dot ${stream === 'live' ? 'health-ok' : 'health-bad'}`} />
            <span style={{ color: '#d5dbdb' }}>{stream === 'live' ? '服务运行中 (实时)' : '连接中断'}</span>
          </div>
          <div style={{ marginTop: 6, color: '#64748b' }}>ASG v1.0 · <a href="https://github.com/dedarek/agent-security-gateway" style={{ color: '#165dff' }}>GitHub</a></div>
        </div>
      </aside>
      <main style={{ overflow: 'auto', minHeight: 0, background: 'var(--bg-0)' }}>
        <Routes>
          <Route path="/" element={<Live streamLive={stream === 'live'} />} />
          <Route path="/agents" element={<Fleet />} />
          <Route path="/policies" element={<Control />} />
          <Route path="/dlp" element={<DlpDemo />} />
          <Route path="/agent/:id" element={<AgentDetail />} />
          <Route path="/fleet/:id" element={<AgentDetail />} />
          <Route path="/insight" element={<Insight />} />
          <Route path="/semantica" element={<SemanticaPage />} />
        </Routes>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Shell />
      </BrowserRouter>
    </QueryClientProvider>
  )
}

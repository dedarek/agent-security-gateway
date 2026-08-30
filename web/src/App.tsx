import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEventStream } from './lib/sse'
import Live from './pages/Live'
import Fleet from './pages/Fleet'
import AgentDetail from './pages/AgentDetail'
import Insight from './pages/Insight'

const qc = new QueryClient()

const NAV = [
  { to: '/', label: '实时台', end: true },
  { to: '/fleet', label: '舰队' },
  { to: '/insight', label: '洞察' },
]

function Shell() {
  const stream = useEventStream()
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '210px 1fr', height: '100vh' }}>
      <aside className="sidebar" style={{ display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '15px 16px 13px', borderBottom: '1px solid rgba(255,255,255,.08)' }}>
          <div className="row" style={{ gap: 10, fontWeight: 800, fontSize: 15, color: '#fff' }}>
            <span style={{ width: 28, height: 28, borderRadius: 6, background: '#ff9900', display: 'grid', placeItems: 'center', color: '#232f3e', fontWeight: 900, fontSize: 15 }}>A</span>
            <span>ASG 控制台</span>
          </div>
          <div style={{ fontSize: 10, color: '#9ba7b4', marginTop: 5, letterSpacing: '0.04em' }}>SaaS</div>
        </div>
        <nav style={{ flex: 1, padding: '10px 8px' }}>
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end}
              className={({ isActive }) => `navlink ${isActive ? 'active' : ''}`}>
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div style={{ padding: '12px 16px', borderTop: '1px solid rgba(255,255,255,.08)', fontSize: 11, color: '#9ba7b4' }}>
          <div className="row" style={{ gap: 6 }}>
            <span className={`health-dot ${stream === 'live' ? 'health-ok' : 'health-bad'}`} />
            <span>{stream === 'live' ? '实时已连接' : '实时断开'}</span>
          </div>
          <div style={{ marginTop: 6 }}>ASG v1 · <a href="https://github.com/dedarek/agent-security-gateway" style={{ color: '#ff9900' }}>GitHub</a></div>
        </div>
      </aside>
      <main style={{ overflow: 'auto', minHeight: 0, background: 'var(--bg-0)' }}>
        <Routes>
          <Route path="/" element={<Live />} />
          <Route path="/fleet" element={<Fleet />} />
          <Route path="/fleet/:id" element={<AgentDetail />} />
          <Route path="/insight" element={<Insight />} />
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

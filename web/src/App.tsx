import { BrowserRouter, NavLink, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEventStream } from './lib/sse'
import Live from './pages/Live'
import Fleet from './pages/Fleet'
import AgentDetail from './pages/AgentDetail'
import Control from './pages/Control'
import Insight from './pages/Insight'

const qc = new QueryClient()

const NAV = [
  { to: '/', label: '实时台', end: true },
  { to: '/fleet', label: '舰队' },
  { to: '/control', label: '管控' },
  { to: '/insight', label: '洞察' },
]

function Shell() {
  const stream = useEventStream()
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '208px 1fr', height: '100vh', background: 'var(--bg-0)' }}>
      <aside style={{ background: 'var(--bg-1)', borderRight: '1px solid var(--line)', display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '16px 16px 14px', borderBottom: '1px solid var(--line)' }}>
          <div className="row" style={{ gap: 10, fontWeight: 800, fontSize: 14 }}>
            <span style={{ width: 30, height: 30, borderRadius: 9, background: 'var(--brand)', display: 'grid', placeItems: 'center', color: '#0d1116', fontWeight: 900 }}>A</span>
            <span>ASG 控制台</span>
          </div>
          <div className="small dim" style={{ marginTop: 6 }}>SaaS · 管控第一 · 可见性第二</div>
        </div>
        <nav style={{ flex: 1, padding: 12 }}>
          {NAV.map((n) => (
            <NavLink key={n.to} to={n.to} end={n.end}
              style={({ isActive }) => ({
                display: 'block', padding: '9px 12px', borderRadius: 'var(--r-s)', marginBottom: 3,
                color: isActive ? 'var(--fg-0)' : 'var(--fg-1)',
                background: isActive ? 'var(--bg-3)' : 'transparent',
                fontWeight: isActive ? 700 : 600, fontSize: 13, textDecoration: 'none',
                borderLeft: isActive ? '3px solid var(--brand)' : '3px solid transparent',
                transition: 'background 140ms var(--ease), color 140ms var(--ease)',
              })}>
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div style={{ padding: 14, borderTop: '1px solid var(--line)' }} className="small dim">
          <div className="row" style={{ gap: 6 }}>
            <span className={`health-dot ${stream === 'live' ? 'health-ok' : 'health-bad'}`} />
            <span>{stream === 'live' ? '实时已连接' : '实时断开'}</span>
          </div>
          <div style={{ marginTop: 6 }}>ASG v1 · <a href="https://github.com/dedarek/agent-security-gateway">GitHub</a></div>
        </div>
      </aside>
      <main style={{ overflow: 'auto', minHeight: 0 }}>
        <Routes>
          <Route path="/" element={<Live />} />
          <Route path="/fleet" element={<Fleet />} />
          <Route path="/fleet/:id" element={<AgentDetail />} />
          <Route path="/control" element={<Control />} />
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

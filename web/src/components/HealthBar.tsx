import { useEffect, useState } from 'react'

type Probe = { name: string; url: string }

const PROBES: Probe[] = [
  { name: 'gateway :8090', url: '/healthz' },
  { name: 'behavior :8901', url: 'http://127.0.0.1:8901/health' },
  { name: 'kg-worker :8902', url: 'http://127.0.0.1:8902/health' },
  { name: 'outputguard :8903', url: 'http://127.0.0.1:8903/health' },
  { name: 'cpolar 公网', url: 'https://asg-gateway.vip.cpolar.cn/healthz' },
]

/** HealthBar pings each sidecar every 10s and shows a pulsing dot per service. */
export function HealthBar() {
  const [ok, setOk] = useState<Record<string, boolean>>({})
  useEffect(() => {
    let dead = false
    const ping = async () => {
      const next: Record<string, boolean> = {}
      await Promise.all(PROBES.map(async (p) => {
        try {
          const ctrl = new AbortController()
          const t = setTimeout(() => ctrl.abort(), 3000)
          const r = await fetch(p.url, { signal: ctrl.signal, mode: p.url.startsWith('http') ? 'cors' : 'same-origin' })
          clearTimeout(t)
          next[p.name] = r.ok
        } catch {
          next[p.name] = false
        }
      }))
      if (!dead) setOk(next)
    }
    ping()
    const iv = setInterval(ping, 10000)
    return () => { dead = true; clearInterval(iv) }
  }, [])

  return (
    <div className="card card-pad row" style={{ gap: 18, flexWrap: 'wrap' }}>
      <span className="h-sec">系统健康</span>
      {PROBES.map((p) => {
        const state = ok[p.name]
        return (
          <span key={p.name} className="row small" style={{ gap: 6 }}>
            <span className={`health-dot ${state === undefined ? '' : state ? 'health-ok' : 'health-bad'}`}
              style={state === undefined ? { background: 'var(--fg-2)' } : undefined} />
            <span className="muted">{p.name}</span>
          </span>
        )
      })}
    </div>
  )
}

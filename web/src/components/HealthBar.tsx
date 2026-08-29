import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

/** HealthBar reads the gateway-aggregated /api/status services map, so the
 * browser never cross-origin fetches sidecars or the public tunnel. */
export function HealthBar() {
  const { data } = useQuery({ queryKey: ['status'], queryFn: api.status, refetchInterval: 10000 })
  const services: Record<string, boolean> = data?.services || {}
  const order = ['gateway', 'behavior', 'kg-worker', 'outputguard', 'cpolar']
  const label: Record<string, string> = {
    gateway: 'gateway :8090', behavior: 'behavior :8901',
    'kg-worker': 'kg-worker :8902', outputguard: 'outputguard :8903', cpolar: 'cpolar 公网',
  }
  return (
    <div className="card card-pad row" style={{ gap: 18, flexWrap: 'wrap' }}>
      <span className="h-sec">系统健康</span>
      {order.map((k) => {
        const state = services[k]
        return (
          <span key={k} className="row small" style={{ gap: 6 }}>
            <span
              className={`health-dot ${state === undefined ? '' : state ? 'health-ok' : 'health-bad'}`}
              style={state === undefined ? { background: 'var(--fg-2)' } : undefined}
            />
            <span className="muted">{label[k] || k}</span>
          </span>
        )
      })}
    </div>
  )
}

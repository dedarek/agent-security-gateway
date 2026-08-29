import { useEffect, useState } from 'react'

/** Number that springs toward its target value (no deps: rAF lerp). */
export function AnimatedNumber({ value, duration = 500 }: { value: number; duration?: number }) {
  const [display, setDisplay] = useState(value)
  useEffect(() => {
    const from = display
    const to = value
    if (from === to) return
    const t0 = performance.now()
    let raf = 0
    const tick = (t: number) => {
      const p = Math.min(1, (t - t0) / duration)
      const eased = 1 - Math.pow(1 - p, 3)
      setDisplay(Math.round(from + (to - from) * eased))
      if (p < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])
  return <span className="kpi-num">{display.toLocaleString()}</span>
}

export type KPIItem = { label: string; value: number; color?: string }

export function KPIBar({ items }: { items: KPIItem[] }) {
  return (
    <div className="row" style={{ gap: 12, flexWrap: 'wrap' }}>
      {items.map((k) => (
        <div key={k.label} className="card kpi" style={{ flex: '1 1 150px', minWidth: 140 }}>
          <div style={{ color: k.color || 'var(--fg-0)' }}>
            <AnimatedNumber value={k.value} />
          </div>
          <div className="kpi-label">{k.label}</div>
        </div>
      ))}
    </div>
  )
}

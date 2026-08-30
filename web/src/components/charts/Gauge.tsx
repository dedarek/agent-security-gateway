/** Semicircle threat gauge, pure SVG. value 0..100. */
export function Gauge({ value, label }: { value: number; label?: string }) {
  const v = Math.max(0, Math.min(100, value))
  const tier = v >= 70 ? { c: 'var(--block)', t: '高危' } : v >= 30 ? { c: 'var(--confirm)', t: '关注' } : { c: 'var(--allow)', t: '平稳' }
  const W = 200, H = 112, cx = W / 2, cy = 96, r = 76
  // inset 6° so 0/100 are not dead-horizontal — needle never sits on the baseline
  const inset = 6 * (Math.PI / 180)
  const span = Math.PI - 2 * inset
  const angle = Math.PI - inset - (v / 100) * span
  const nx = cx + (r - 10) * Math.cos(angle)
  const ny = cy - (r - 10) * Math.sin(angle)
  const arc = (from: number, to: number) => {
    const a0 = Math.PI - inset - (from / 100) * span
    const a1 = Math.PI - inset - (to / 100) * span
    const x0 = cx + r * Math.cos(a0), y0 = cy - r * Math.sin(a0)
    const x1 = cx + r * Math.cos(a1), y1 = cy - r * Math.sin(a1)
    const large = 0
    return `M ${x0} ${y0} A ${r} ${r} 0 0 1 ${x1} ${y1}`
  }
  return (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} role="img" aria-label="threat gauge" style={{ overflow: 'visible' }}>
      {/* track */}
      <path d={arc(0, 100)} fill="none" stroke="#e8eaed" strokeWidth={14} strokeLinecap="round" />
      {/* faint scale segments */}
      <path d={arc(0, 30)} fill="none" stroke="var(--allow)" strokeWidth={14} opacity={0.22} strokeLinecap="butt" />
      <path d={arc(30, 70)} fill="none" stroke="var(--confirm)" strokeWidth={14} opacity={0.22} />
      <path d={arc(70, 100)} fill="none" stroke="var(--block)" strokeWidth={14} opacity={0.22} strokeLinecap="butt" />
      {/* value arc */}
      <path d={arc(0, v)} fill="none" stroke={tier.c} strokeWidth={14} strokeLinecap="round" style={{ transition: 'all 500ms var(--ease)' }} />
      {/* tick marks */}
      {[0, 30, 70, 100].map((p) => {
        const a = Math.PI - inset - (p / 100) * span
        const x0 = cx + (r - 2) * Math.cos(a), y0 = cy - (r - 2) * Math.sin(a)
        const x1 = cx + (r + 4) * Math.cos(a), y1 = cy - (r + 4) * Math.sin(a)
        return <line key={p} x1={x0} y1={y0} x2={x1} y2={y1} stroke="#9aa0a6" strokeWidth={1.2} opacity={0.9} />
      })}
      {/* needle */}
      <line x1={cx} y1={cy} x2={nx} y2={ny} stroke={tier.c} strokeWidth={3.5} strokeLinecap="round" style={{ transition: 'all 500ms var(--ease)' }} />
      <circle cx={cx} cy={cy} r={7} fill={tier.c} stroke="#fff" strokeWidth={2.5} />
      <circle cx={nx} cy={ny} r={4.5} fill={tier.c} stroke="#fff" strokeWidth={1.5} />
      <text x={cx} y={cy - 20} textAnchor="middle" fontSize={28} fontWeight={800} fill="var(--fg-0)" fontVariantNumeric="tabular-nums">{Math.round(v)}</text>
      <text x={cx} y={cy + 18} textAnchor="middle" fontSize={11} fontWeight={700} fill={tier.c} letterSpacing="0.02em">{label || tier.t}</text>
    </svg>
  )
}

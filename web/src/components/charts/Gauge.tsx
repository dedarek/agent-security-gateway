/** Semicircle threat gauge, pure SVG. value 0..100. */
export function Gauge({ value, label }: { value: number; label?: string }) {
  const v = Math.max(0, Math.min(100, value))
  const tier = v >= 70 ? { c: 'var(--block)', t: '高危' } : v >= 30 ? { c: 'var(--confirm)', t: '关注' } : { c: 'var(--allow)', t: '平稳' }
  const W = 180, H = 100, cx = W / 2, cy = 92, r = 74
  const angle = Math.PI * (1 - v / 100) // π → 0
  const nx = cx + (r - 16) * Math.cos(angle)
  const ny = cy - (r - 16) * Math.sin(angle)
  const arc = (from: number, to: number) => {
    const a0 = Math.PI * (1 - from / 100), a1 = Math.PI * (1 - to / 100)
    const x0 = cx + r * Math.cos(a0), y0 = cy - r * Math.sin(a0)
    const x1 = cx + r * Math.cos(a1), y1 = cy - r * Math.sin(a1)
    return `M ${x0} ${y0} A ${r} ${r} 0 0 1 ${x1} ${y1}`
  }
  return (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} role="img" aria-label="threat gauge">
      <path d={arc(0, 100)} fill="none" stroke="var(--bg-3)" strokeWidth={12} strokeLinecap="round" />
      <path d={arc(0, 30)} fill="none" stroke="var(--allow)" strokeWidth={12} opacity={0.4} strokeLinecap="round" />
      <path d={arc(30, 70)} fill="none" stroke="var(--confirm)" strokeWidth={12} opacity={0.4} />
      <path d={arc(70, 100)} fill="none" stroke="var(--block)" strokeWidth={12} opacity={0.4} strokeLinecap="round" />
      <path d={arc(0, v)} fill="none" stroke={tier.c} strokeWidth={12} strokeLinecap="round" style={{ transition: 'all 500ms var(--ease)' }} />
      <line x1={cx} y1={cy} x2={nx} y2={ny} stroke={tier.c} strokeWidth={3} strokeLinecap="round" style={{ transition: 'all 500ms var(--ease)' }} />
      <circle cx={cx} cy={cy} r={6} fill={tier.c} />
      <text x={cx} y={cy - 22} textAnchor="middle" fontSize={26} fontWeight={800} fill="var(--fg-0)" fontVariantNumeric="tabular-nums">{Math.round(v)}</text>
      <text x={cx} y={cy + 16} textAnchor="middle" fontSize={11} fontWeight={700} fill={tier.c}>{label || tier.t}</text>
    </svg>
  )
}

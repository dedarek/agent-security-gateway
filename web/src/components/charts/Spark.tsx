/** Sparkline of per-minute event counts fed by SSE (the only truly real-time chart). */
export function Spark({ points, height = 44 }: { points: number[]; height?: number }) {
  const W = 240
  const n = Math.max(2, points.length)
  const peak = Math.max(1, ...points)
  const x = (i: number) => (i / (n - 1)) * W
  const y = (v: number) => height - 4 - (v / peak) * (height - 8)
  const line = points.map((v, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(' ')
  const area = `${line} L ${W} ${height} L 0 ${height} Z`
  const lastX = x(points.length - 1)
  const lastY = y(points[points.length - 1] ?? 0)
  return (
    <svg viewBox={`0 0 ${W} ${height}`} width="100%" height={height} role="img" aria-label="activity sparkline" style={{ display: 'block' }}>
      <path d={area} fill="var(--brand)" opacity={0.15} />
      <path d={line} fill="none" stroke="var(--brand)" strokeWidth={1.6} />
      <circle cx={lastX} cy={lastY} r={3.5} fill="var(--brand)">
        <animate attributeName="r" values="2.5;5;2.5" dur="1.6s" repeatCount="indefinite" />
        <animate attributeName="opacity" values="1;0.3;1" dur="1.6s" repeatCount="indefinite" />
      </circle>
    </svg>
  )
}

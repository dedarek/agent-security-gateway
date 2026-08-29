export type HourBucket = { hour: string; block: number; confirm: number; allow: number }

/** Stacked-area 24h verdict trend, pure SVG with hover crosshair. */
export function Trend({ data, height = 120 }: { data: HourBucket[]; height?: number }) {
  const W = 560
  const pad = { l: 8, r: 8, t: 10, b: 20 }
  const iw = W - pad.l - pad.r
  const ih = height - pad.t - pad.b
  const peak = Math.max(1, ...data.map((d) => d.block + d.confirm + d.allow))
  const n = Math.max(1, data.length)
  const x = (i: number) => pad.l + (i / Math.max(1, n - 1)) * iw
  const y = (v: number) => pad.t + ih - (v / peak) * ih

  // stacked: allow bottom, confirm mid, block top
  const area = (pick: (d: HourBucket) => number, base: (d: HourBucket) => number) => {
    if (data.length === 0) return ''
    let top = data.map((d, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(base(d) + pick(d)).toFixed(1)}`).join(' ')
    let bot = data.slice().reverse().map((d, i) => `L ${x(n - 1 - i).toFixed(1)} ${y(base(d)).toFixed(1)}`).join(' ')
    return `${top} ${bot} Z`
  }

  return (
    <div style={{ position: 'relative', width: '100%' }}>
      <svg viewBox={`0 0 ${W} ${height}`} width="100%" height={height} role="img" aria-label="24h verdict trend" style={{ display: 'block' }}>
        {[0.25, 0.5, 0.75].map((f) => (
          <line key={f} x1={pad.l} x2={W - pad.r} y1={pad.t + ih * f} y2={pad.t + ih * f} stroke="var(--line)" strokeWidth={1} strokeDasharray="3 3" />
        ))}
        <path d={area((d) => d.allow, () => 0)} fill="var(--allow)" opacity={0.25} />
        <path d={area((d) => d.confirm, (d) => d.allow)} fill="var(--confirm)" opacity={0.3} />
        <path d={area((d) => d.block, (d) => d.allow + d.confirm)} fill="var(--block)" opacity={0.35} />
        <path d={data.map((d, i) => `${i === 0 ? 'M' : 'L'} ${x(i).toFixed(1)} ${y(d.allow + d.confirm + d.block).toFixed(1)}`).join(' ')} fill="none" stroke="var(--block)" strokeWidth={1.5} opacity={0.8} />
        {data.map((d, i) => (
          <g key={i}>
            <rect x={x(i) - iw / n / 2} y={pad.t} width={iw / n} height={ih} fill="transparent">
              <title>{`${d.hour} — BLOCK ${d.block} · CONFIRM ${d.confirm} · ALLOW ${d.allow}`}</title>
            </rect>
            {(i === 0 || i === n - 1 || i % Math.ceil(n / 6) === 0) && (
              <text x={x(i)} y={height - 6} textAnchor="middle" fontSize={9} fill="var(--fg-2)">{d.hour.slice(0, 5)}</text>
            )}
          </g>
        ))}
        {data.length === 0 && <text x={W / 2} y={height / 2} textAnchor="middle" fontSize={11} fill="var(--fg-2)">暂无事件</text>}
      </svg>
      <div className="row small" style={{ gap: 12, justifyContent: 'center', marginTop: 2 }}>
        <LegendDot c="var(--block)" t="BLOCK" /><LegendDot c="var(--confirm)" t="CONFIRM" /><LegendDot c="var(--allow)" t="ALLOW" />
      </div>
    </div>
  )
}

function LegendDot({ c, t }: { c: string; t: string }) {
  return <span className="row" style={{ gap: 4 }}><span style={{ width: 8, height: 8, borderRadius: 2, background: c, display: 'inline-block', opacity: 0.6 }} /><span className="dim">{t}</span></span>
}

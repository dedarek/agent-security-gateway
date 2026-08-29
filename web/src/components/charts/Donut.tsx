import { useId } from 'react'

export type Slice = { label: string; value: number; color: string }

/** Donut ring chart, pure SVG. Click a slice to filter; center shows total. */
export function Donut({ slices, size = 148, onSelect, centerLabel }: {
  slices: Slice[]
  size?: number
  onSelect?: (label: string) => void
  centerLabel?: string
}) {
  const total = slices.reduce((a, s) => a + s.value, 0)
  const id = useId()
  const r = 54
  const cx = size / 2
  const cy = size / 2
  const C = 2 * Math.PI * r
  let acc = 0
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label="donut">
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--bg-3)" strokeWidth={16} />
      {total > 0 && slices.map((s) => {
        const frac = s.value / total
        const dash = frac * C
        const gap = C - dash
        const offset = -acc * C
        acc += frac
        return (
          <circle
            key={s.label}
            cx={cx} cy={cy} r={r} fill="none"
            stroke={s.color} strokeWidth={16}
            strokeDasharray={`${dash} ${gap}`}
            strokeDashoffset={offset}
            strokeLinecap="butt"
            transform={`rotate(-90 ${cx} ${cy})`}
            style={{ cursor: onSelect ? 'pointer' : 'default', transition: 'stroke-width 160ms var(--ease)', opacity: 0.95 }}
            onMouseEnter={(e) => ((e.target as SVGCircleElement).style.strokeWidth = '20')}
            onMouseLeave={(e) => ((e.target as SVGCircleElement).style.strokeWidth = '16')}
            onClick={() => onSelect?.(s.label)}
          >
            <title>{`${s.label}: ${s.value} (${Math.round(frac * 100)}%)`}</title>
          </circle>
        )
      })}
      <text x={cx} y={cy - 4} textAnchor="middle" fontSize={26} fontWeight={800} fill="var(--fg-0)" fontVariantNumeric="tabular-nums">{total}</text>
      <text x={cx} y={cy + 16} textAnchor="middle" fontSize={10} fill="var(--fg-2)">{centerLabel || '总数'}</text>
      <desc id={id}>{slices.map((s) => `${s.label} ${s.value}`).join(', ')}</desc>
    </svg>
  )
}

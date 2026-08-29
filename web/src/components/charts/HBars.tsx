export type BarItem = { label: string; value: number; color?: string; warn?: boolean }

/** Horizontal bar list, pure SVG-free (flex bars). Click a row to filter. */
export function HBars({ items, onSelect, max }: { items: BarItem[]; onSelect?: (label: string) => void; max?: number }) {
  const peak = max ?? Math.max(1, ...items.map((i) => i.value))
  return (
    <div className="col" style={{ gap: 8 }}>
      {items.map((it) => (
        <button
          key={it.label}
          onClick={() => onSelect?.(it.label)}
          style={{ background: 'none', border: 'none', padding: 0, cursor: onSelect ? 'pointer' : 'default', textAlign: 'left', width: '100%' }}
          title={`${it.label}: ${it.value}`}
        >
          <div className="row-between" style={{ marginBottom: 3 }}>
            <span className="small" style={{ fontWeight: 600, color: 'var(--fg-0)' }}>
              {it.warn && <span style={{ color: 'var(--confirm)', marginRight: 4 }}>⚠</span>}{it.label}
            </span>
            <span className="small dim mono">{it.value}</span>
          </div>
          <div style={{ height: 8, background: 'var(--bg-3)', borderRadius: 4, overflow: 'hidden' }}>
            <div style={{
              height: '100%', width: `${(it.value / peak) * 100}%`,
              background: it.color || 'var(--brand)', borderRadius: 4,
              transition: 'width 500ms var(--ease)',
            }} />
          </div>
        </button>
      ))}
    </div>
  )
}

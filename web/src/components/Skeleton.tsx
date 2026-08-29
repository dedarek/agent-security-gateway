export function Skeleton({ w = '100%', h = 14, style }: { w?: number | string; h?: number; style?: React.CSSProperties }) {
  return <div className="skeleton" style={{ width: w, height: h, ...style }} />
}

export function SkeletonRows({ n = 5, h = 34 }: { n?: number; h?: number }) {
  return (
    <div className="col" style={{ gap: 8, padding: 12 }}>
      {Array.from({ length: n }).map((_, i) => (
        <Skeleton key={i} h={h} />
      ))}
    </div>
  )
}

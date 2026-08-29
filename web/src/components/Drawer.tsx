import { useEffect, useRef, type ReactNode } from 'react'

export function Drawer({ open, onClose, title, children }: {
  open: boolean
  onClose: () => void
  title?: ReactNode
  children: ReactNode
}) {
  const bodyRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    if (open) window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null
  return (
    <>
      <div className="drawer-mask" onClick={onClose} />
      <div className="drawer slide-in" style={{ animation: 'none', transform: 'translateX(0)', transition: 'transform 260ms var(--ease)' }}>
        <div className="drawer-head row-between">
          <div style={{ fontWeight: 700, fontSize: 14 }}>{title}</div>
          <button className="btn btn-ghost" onClick={onClose} aria-label="关闭">✕</button>
        </div>
        <div className="drawer-body" ref={bodyRef}>{children}</div>
      </div>
    </>
  )
}

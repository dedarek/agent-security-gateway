export function StatusDot({ status }: { status: string }) {
  const normalized = (status || '').toLowerCase()
  const cls = normalized === 'active' ? 'dot dot-active' : normalized === 'idle' ? 'dot dot-idle' : 'dot dot-offline'
  const label = normalized === 'active' ? '活跃' : normalized === 'idle' ? '闲置' : '离线'
  return <span className="row" style={{ gap: 6 }}><span className={cls} /><span className="small" style={{ fontWeight: 600, color: normalized === 'active' ? 'var(--allow)' : normalized === 'idle' ? 'var(--confirm)' : 'var(--fg-2)' }}>{label}</span></span>
}

export function statusLabel(s: string): string {
  const n = (s || '').toLowerCase()
  return n === 'active' ? '活跃' : n === 'idle' ? '闲置' : '离线'
}

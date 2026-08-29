export function EmptyState({ icon = '○', title, hint, action }: { icon?: string; title: string; hint?: string; action?: React.ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <div style={{ fontWeight: 600, color: 'var(--fg-1)' }}>{title}</div>
      {hint && <div className="small dim" style={{ maxWidth: 360 }}>{hint}</div>}
      {action}
    </div>
  )
}

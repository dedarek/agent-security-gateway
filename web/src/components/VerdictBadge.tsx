export function VerdictBadge({ v }: { v?: string }) {
  const val = (v || 'ALLOW').toUpperCase()
  const cls =
    val === 'BLOCK' ? 'badge-block' :
    val === 'CONFIRM' ? 'badge-confirm' :
    val === 'REDACT' ? 'badge-redact' : 'badge-allow'
  return <span className={`badge ${cls}`}>{val}</span>
}

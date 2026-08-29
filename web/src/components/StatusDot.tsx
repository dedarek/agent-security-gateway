export function StatusDot({ status }: { status: string }) {
  const cls = status === 'active' ? 'dot dot-active' : status === 'idle' ? 'dot dot-idle' : 'dot dot-offline'
  return <span className={cls} title={status} />
}

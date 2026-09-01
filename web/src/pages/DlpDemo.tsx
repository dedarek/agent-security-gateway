import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { VerdictBadge } from '../components/VerdictBadge'
import { EmptyState } from '../components/EmptyState'

/** DlpDemo — 跨工具数据流 Demo：敏感数据从 .env 流到外发动作被 BLOCK。
 * 展示 ASG 的 runtime enforcement（不是事后审计）：执行前阻断 + 完整数据血缘。 */
export default function DlpDemo() {
  const { data: events } = useQuery({ queryKey: ['events'], queryFn: api.events, refetchInterval: 3000 })
  const { data: daData } = useQuery({ queryKey: ['data-access-all'], queryFn: () => api.dataAccessRecent(), refetchInterval: 3000 })
  const [focus, setFocus] = useState<any>(null)

  // ---- 组装数据流链：从 data_access hops 里找"credential taint + BLOCK"的链路 ----
  const hops = daData?.hops || []
  const blocked = hops.filter((h: any) => h.decision === 'BLOCK')
  const chain: any[] = []
  if (blocked.length > 0) {
    const b = blocked[0]
    // 找最近的 read 前驱（不限同 trace——hook 路径 trace 是 per-tool 的）
    const readHop = hops.find((h: any) => h.operation === 'read' && h.data_class)
    const writeHops = hops.filter((h: any) => h.operation !== 'read')
    const sink = writeHops.find((h: any) => h.operation === 'transmit' && h.decision === 'BLOCK')
    const src = readHop?.source || b.source || 'sensitive source'
    chain.push({ kind: 'agent', label: 'Claude Code', icon: '🤖' })
    if (readHop) chain.push({ kind: 'tool', label: readHop.tool_id, op: 'read', decision: readHop.decision, data: readHop })
    chain.push({ kind: 'data', label: src, cls: readHop?.data_class || b.data_class || 'credential', taint: true })
    chain.push({ kind: 'flow', label: 'sensitive data flow' })
    if (sink) {
      chain.push({ kind: 'tool', label: sink.tool_id, op: sink.operation, decision: sink.decision, data: sink })
      if (sink.destination) chain.push({ kind: 'sink', label: sink.destination, external: sink.trust_zone_dst === 'external', data: sink })
    } else {
      chain.push({ kind: 'tool', label: b.tool_id, op: b.operation, decision: b.decision, data: b })
    }
  }

  // ---- 时间轴：从 events 里提取 hook 工具事件 ----
  const evs = events || []
  const hookEvents = evs.filter((e: any) => (e.Call?.ToolID || '').startsWith('hook.')).slice(-12)
  const timeline = hookEvents.map((e: any) => ({
    ts: e.Timestamp ? new Date(e.Timestamp).toLocaleTimeString('zh-CN', { hour12: false }) : '',
    tool: (e.Call?.ToolID || '').replace('hook.', ''),
    verdict: e.Decision?.Final || 'ALLOW',
    reason: e.Decision?.Rationale || '',
  }))

  return (
    <div style={{ padding: 24, maxWidth: 1100, margin: '0 auto' }}>
      <div className="row-between" style={{ marginBottom: 16 }}>
        <div>
          <h1 className="h-page" style={{ fontSize: 22, fontWeight: 600 }}>DLP 数据流 Demo</h1>
          <div className="small dim">敏感数据从读取 → 本地处理 → 外发被阻断的完整链路（runtime enforcement）</div>
        </div>
        <span className="badge" style={{ color: 'var(--block)', borderColor: 'var(--block)' }}>runtime enforcement</span>
      </div>

      {/* ===== 数据流链 ===== */}
      <div className="card card-pad" style={{ marginBottom: 16 }}>
        <div className="h-sec" style={{ marginBottom: 12 }}>数据血缘 <span className="dim">(Data Lineage)</span></div>
        {chain.length === 0 ? (
          <EmptyState icon="◌" title="暂无 DLP 阻断链路" hint="触发一次：让 Claude 读 .env 再 curl 外部地址，这里会实时显示完整数据流。" />
        ) : (
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
            {chain.map((n: any, i: number) => (
              <span key={i} style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
                {i > 0 && <span style={{ color: 'var(--fg-3)' }}>→</span>}
                {n.kind === 'flow' ? (
                  <span className="badge" style={{ color: 'var(--warn)', borderColor: 'var(--warn)', fontStyle: 'italic' }}>{n.label}</span>
                ) : n.kind === 'sink' ? (
                  <span className="chip" style={{ color: 'var(--block)', borderColor: 'var(--block)', cursor: 'pointer' }} onClick={() => setFocus(n.data)}>
                    ⚡ {n.label} <span className="dim">(external)</span>
                  </span>
                ) : n.kind === 'data' ? (
                  <span className="chip" style={{ color: 'var(--warn)', borderColor: 'var(--warn)', cursor: 'pointer' }} onClick={() => setFocus(n.data)}>
                    🔒 {n.label} <span className="dim">({n.cls})</span>
                  </span>
                ) : n.kind === 'agent' ? (
                  <span className="chip">{n.icon} {n.label}</span>
                ) : (
                  <span className="chip" style={{ cursor: 'pointer', borderColor: n.decision === 'BLOCK' ? 'var(--block)' : 'var(--line)' }} onClick={() => setFocus(n.data)}>
                    {n.label} {n.op && <span className="dim">({n.op})</span>}
                    <VerdictBadge v={n.decision} />
                  </span>
                )}
              </span>
            ))}
          </div>
        )}
      </div>

      {/* ===== 阻断详情 ===== */}
      {focus && (
        <div className="card card-pad" style={{ marginBottom: 16, borderColor: 'var(--block)', borderWidth: 1 }}>
          <div className="h-sec" style={{ marginBottom: 10, color: 'var(--block)' }}>🚫 阻断详情 — execution prevented before tool run</div>
          <div className="kv">
            <dt>Decision</dt><dd><VerdictBadge v={focus.decision} /></dd>
            <dt>Reason</dt><dd style={{ color: 'var(--block)' }}>{focus.decision === 'BLOCK' ? 'Session carries sensitive data and current operation transmits externally' : (focus.decision || '-')}</dd>
            <dt>Source</dt><dd className="mono">{focus.source || '-（同 trace 前驱 Read）'}</dd>
            <dt>Classification</dt><dd>{focus.data_class || 'credential'}</dd>
            <dt>Destination</dt><dd className="mono" style={{ color: focus.trust_zone_dst === 'external' ? 'var(--block)' : 'inherit' }}>{focus.destination || '-'}</dd>
            <dt>Trust zone</dt><dd style={{ color: focus.trust_zone_dst === 'external' ? 'var(--block)' : 'inherit' }}>{focus.trust_zone_dst || 'local'}</dd>
            <dt>Tool</dt><dd>{focus.tool_id}</dd>
            <dt>Trace</dt><dd className="mono small">{focus.trace_id || '-'}</dd>
            <dt>Taint</dt><dd>{(focus.taint_tags || []).length ? focus.taint_tags.join(', ') : 'credential'}</dd>
          </div>
        </div>
      )}

      {/* ===== 时间轴 ===== */}
      <div className="card card-pad">
        <div className="h-sec" style={{ marginBottom: 10 }}>实时时间轴</div>
        {timeline.length === 0 ? (
          <div className="dim small">等待 hook 事件…</div>
        ) : (
          <div className="timeline">
            {timeline.slice().reverse().map((t: any, i: number) => (
              <div key={i} className={`timeline-item t-${(t.verdict || 'ALLOW').toLowerCase()}`}>
                <div className="row" style={{ gap: 10, flexWrap: 'wrap' }}>
                  <span className="small dim mono">{t.ts}</span>
                  <span className="chip">{t.tool}</span>
                  <VerdictBadge v={t.verdict} />
                </div>
                {t.reason && <div className="small" style={{ color: 'var(--block)', marginTop: 2 }}>{t.reason}</div>}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

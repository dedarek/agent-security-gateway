import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Agent } from './types'

export type StreamStep = {
  at: string
  agent_id: string
  session_id: string
  kind: string
  tool_name: string
  summary: string
  verdict: string
  reason?: string
}

export type StreamStatus = 'connecting' | 'live' | 'down'

/**
 * useEventStream subscribes to /api/stream and:
 *  - "agents"   → replaces the react-query ['agents'] cache (real-time fleet)
 *  - "activity" → appends to a local ring buffer consumed by the live feed
 * Reconnects with exponential backoff (1s→2s→…→15s cap). Falls back: pages
 * keep their refetchInterval polling, so a dead stream degrades gracefully.
 */
export function useEventStream(onActivity?: (step: StreamStep) => void): StreamStatus {
  const qc = useQueryClient()
  const [status, setStatus] = useState<StreamStatus>('connecting')
  const cbRef = useRef(onActivity)

  useEffect(() => {
    cbRef.current = onActivity
  }, [onActivity])

  useEffect(() => {
    let es: EventSource | null = null
    let retry = 1000
    let timer: ReturnType<typeof setTimeout> | null = null
    let dead = false

    const connect = () => {
      if (dead) return
      es = new EventSource('/api/stream')
      es.onopen = () => {
        retry = 1000
        setStatus('live')
      }
      es.addEventListener('agents', (e) => {
        try {
          const agents = JSON.parse((e as MessageEvent).data) as Agent[]
          qc.setQueryData(['agents'], agents)
        } catch { /* ignore malformed */ }
      })
      es.addEventListener('activity', (e) => {
        try {
          const step = JSON.parse((e as MessageEvent).data) as StreamStep
          cbRef.current?.(step)
        } catch { /* ignore malformed */ }
      })
      es.onerror = () => {
        setStatus('down')
        es?.close()
        if (!dead) {
          timer = setTimeout(connect, retry)
          retry = Math.min(retry * 2, 15000)
        }
      }
    }
    connect()
    return () => {
      dead = true
      if (timer) clearTimeout(timer)
      es?.close()
    }
  }, [qc])

  return status
}

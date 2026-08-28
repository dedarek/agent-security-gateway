import { useEffect, useRef } from 'react'

export function useSSE(url: string, onMessage: (event: string, data: any) => void, enabled = true) {
  const ref = useRef<EventSource | null>(null)
  useEffect(() => {
    if (!enabled) return
    // For now, placeholder: SSE not yet implemented on server, fallback to polling
    // When /api/stream is available, this will connect
    const es = new EventSource(url)
    ref.current = es
    es.onmessage = (e) => {
      try {
        onMessage('message', JSON.parse(e.data))
      } catch {
        onMessage('message', e.data)
      }
    }
    es.onerror = () => {
      es.close()
    }
    return () => es.close()
  }, [url, enabled])
}

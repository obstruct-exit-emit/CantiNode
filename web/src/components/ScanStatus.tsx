import { useEffect, useState } from 'react'
import { getScanStatus, triggerScan, type ScanState } from '../api'

const POLL_MS = 3000

export function ScanStatus({ apiKey, onScanFinished }: { apiKey: string; onScanFinished?: () => void }) {
  const [state, setState] = useState<ScanState | null>(null)
  const wasRunning = state?.running ?? false

  useEffect(() => {
    let cancelled = false
    async function poll() {
      try {
        const s = await getScanStatus(apiKey)
        if (cancelled) return
        setState((prev) => {
          if (prev?.running && !s.running) onScanFinished?.()
          return s
        })
      } catch {
        // transient — next poll tries again
      }
    }
    poll()
    const id = setInterval(poll, POLL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiKey])

  async function handleTrigger() {
    try {
      await triggerScan(apiKey)
      setState((prev) => (prev ? { ...prev, running: true } : prev))
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  const running = state?.running ?? wasRunning

  return (
    <div className="scan-status">
      {state?.result && !running && (
        <span className="text-muted" title={state.finished_at ? `Last scan finished ${new Date(state.finished_at).toLocaleString()}` : undefined}>
          {state.result.FilesMatched}/{state.result.FilesFound} matched
        </span>
      )}
      <button className="scan-btn" onClick={handleTrigger} disabled={running}>
        {running ? 'Scanning…' : 'Scan now'}
      </button>
    </div>
  )
}

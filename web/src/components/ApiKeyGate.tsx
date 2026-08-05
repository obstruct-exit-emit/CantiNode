import { useState } from 'react'
import { ApiError, getVersion, storeApiKey } from '../api'

// ApiKeyGate is CantiNode's whole v1 auth UI: there's no login-account
// system yet (see ROADMAP.md), just the single API key printed to the
// log/config.yaml on first run — this just asks for it once and verifies
// it actually works before storing it.
export function ApiKeyGate({ onVerified }: { onVerified: (apiKey: string) => void }) {
  const [value, setValue] = useState('')
  const [error, setError] = useState<string | undefined>(undefined)
  const [checking, setChecking] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const key = value.trim()
    if (!key) return
    setChecking(true)
    setError(undefined)
    try {
      await getVersion(key)
      storeApiKey(key)
      onVerified(key)
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? 'That key was rejected.' : err instanceof Error ? err.message : String(err))
    } finally {
      setChecking(false)
    }
  }

  return (
    <div className="gate">
      <div className="gate-card">
        <h1>🎵 CantiNode</h1>
        <p>
          Enter your API key to continue. It was printed to the terminal/log when CantiNode first started, and is
          also in <code>config.yaml</code> (<code>api_key</code>).
        </p>
        <form onSubmit={handleSubmit}>
          <input
            type="password"
            placeholder="API key"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoFocus
          />
          {error && <p className="gate-error">{error}</p>}
          <button type="submit" disabled={checking || !value.trim()}>
            {checking ? 'Checking…' : 'Continue'}
          </button>
        </form>
      </div>
    </div>
  )
}

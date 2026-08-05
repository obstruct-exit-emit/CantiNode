import { useEffect, useState } from 'react'
import { getSettings, updateSettings, type Settings as SettingsType } from '../api'

export function Settings({ apiKey }: { apiKey: string }) {
  const [settings, setSettings] = useState<SettingsType | null>(null)
  const [error, setError] = useState<string | undefined>(undefined)
  const [success, setSuccess] = useState(false)
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    getSettings(apiKey)
      .then(setSettings)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }, [apiKey])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!settings) return
    setSaving(true)
    setError(undefined)
    setSuccess(false)
    try {
      const updated = await updateSettings(apiKey, settings)
      setSettings(updated)
      setSuccess(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  function copyKey() {
    if (!settings) return
    navigator.clipboard.writeText(settings.api_key).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  if (!settings) return error ? <p className="load-error">{error}</p> : null

  return (
    <div className="settings">
      <div className="settings-card">
        <h2>API key</h2>
        <div className="api-key-row">
          <span className="api-key-value mono">{settings.api_key}</span>
          <button onClick={copyKey}>{copied ? 'Copied!' : 'Copy'}</button>
        </div>
        <p className="settings-help">Used by scripts and the web UI itself (Authorization: Bearer). Change it directly in config.yaml.</p>
      </div>

      <div className="settings-card">
        <h2>General</h2>
        <form className="general-form" onSubmit={handleSubmit}>
          <label>
            Log level
            <select value={settings.log_level} onChange={(e) => setSettings({ ...settings, log_level: e.target.value })}>
              <option value="debug">debug</option>
              <option value="info">info</option>
              <option value="warn">warn</option>
              <option value="error">error</option>
            </select>
          </label>

          <label>
            Scan interval (hours)
            <input
              type="number"
              min={1}
              value={settings.scan_interval_hours}
              onChange={(e) => setSettings({ ...settings, scan_interval_hours: Number(e.target.value) })}
            />
          </label>

          <label>
            Naming format
            <input
              type="text"
              value={settings.naming_format}
              onChange={(e) => setSettings({ ...settings, naming_format: e.target.value })}
            />
          </label>
          <p className="settings-help">
            Placeholders: <code>{'{Artist}'}</code> <code>{'{Album}'}</code> <code>{'{Year}'}</code>{' '}
            <code>{'{TrackNumber}'}</code> <code>{'{DiscNumber}'}</code> <code>{'{Title}'}</code> <code>{'{Ext}'}</code>
          </p>

          <label>
            Minimum match confidence ({settings.min_match_confidence.toFixed(2)})
            <input
              type="range"
              min={0}
              max={1}
              step={0.05}
              value={settings.min_match_confidence}
              onChange={(e) => setSettings({ ...settings, min_match_confidence: Number(e.target.value) })}
            />
          </label>

          <label className="toggle-row">
            <input
              type="checkbox"
              checked={settings.organize_on_match}
              onChange={(e) => setSettings({ ...settings, organize_on_match: e.target.checked })}
            />
            Organize a file automatically as soon as it's matched
          </label>

          <label>
            MusicBrainz contact email (optional)
            <input
              type="text"
              placeholder="you@example.com"
              value={settings.musicbrainz_contact_email}
              onChange={(e) => setSettings({ ...settings, musicbrainz_contact_email: e.target.value })}
            />
          </label>

          <button type="submit" disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          {success && <p className="settings-success">Saved.</p>}
          {error && <p className="settings-error">{error}</p>}
        </form>
      </div>
    </div>
  )
}

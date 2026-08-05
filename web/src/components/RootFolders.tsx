import { useEffect, useState } from 'react'
import { ApiError, createRootFolder, deleteRootFolder, listRootFolders, type RootFolder } from '../api'
import { DirectoryBrowser } from './DirectoryBrowser'

export function RootFolders({ apiKey, onChanged }: { apiKey: string; onChanged?: () => void }) {
  const [folders, setFolders] = useState<RootFolder[]>([])
  const [path, setPath] = useState('')
  const [error, setError] = useState<string | undefined>(undefined)
  const [submitting, setSubmitting] = useState(false)
  const [browsing, setBrowsing] = useState(false)

  function refresh() {
    listRootFolders(apiKey)
      .then(setFolders)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  useEffect(refresh, [apiKey])

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = path.trim()
    if (!trimmed) return
    setSubmitting(true)
    setError(undefined)
    try {
      await createRootFolder(apiKey, trimmed)
      setPath('')
      refresh()
      onChanged?.()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDelete(id: number) {
    if (!confirm('Remove this root folder? Its scanned files are forgotten (matched artists/albums/tracks stay, in case another root folder still references them); nothing on disk is touched.')) return
    try {
      await deleteRootFolder(apiKey, id)
      refresh()
      onChanged?.()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="root-folders">
      <div className="settings-card">
        <h2>Root folders</h2>
        <p className="settings-help">
          Folders CantiNode scans for audio files. The path must already exist and be readable by CantiNode — it
          organizes an existing library, not create one from nothing.
        </p>
        <form onSubmit={handleAdd}>
          <input
            type="text"
            placeholder="/path/to/music"
            value={path}
            onChange={(e) => setPath(e.target.value)}
          />
          <button type="button" onClick={() => setBrowsing(true)}>
            Browse…
          </button>
          <button type="submit" disabled={submitting || !path.trim()}>
            Add
          </button>
        </form>
        {error && <p className="settings-error">{error}</p>}

        {browsing && (
          <DirectoryBrowser
            apiKey={apiKey}
            startPath={path}
            onClose={() => setBrowsing(false)}
            onSelect={(selected) => {
              setPath(selected)
              setBrowsing(false)
            }}
          />
        )}

        {folders.length === 0 ? (
          <p className="empty">No root folders yet. Add one above to start scanning.</p>
        ) : (
          <ul className="rows">
            {folders.map((f) => (
              <li className="row" key={f.id}>
                <span className="user-row-name mono">{f.path}</span>
                <button className="toggle" onClick={() => handleDelete(f.id)}>
                  Remove
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

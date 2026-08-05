import { useEffect, useState } from 'react'
import { browseDirectories, type BrowseEntry } from '../api'

// DirectoryBrowser is a server-side directory picker modal — CantiNode
// organizes files on the machine it runs on, not the browser's own
// filesystem, so a plain <input type="file" webkitdirectory> can't be
// used here; this instead walks the server's own filesystem one level at
// a time, the same convention Sonarr/Radarr/Lidarr use for adding root
// folders.
export function DirectoryBrowser({
  apiKey,
  startPath,
  onSelect,
  onClose,
}: {
  apiKey: string
  startPath: string
  onSelect: (path: string) => void
  onClose: () => void
}) {
  const [path, setPath] = useState(startPath)
  const [parent, setParent] = useState<string | null>(null)
  const [entries, setEntries] = useState<BrowseEntry[]>([])
  const [error, setError] = useState<string | undefined>(undefined)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    setError(undefined)
    browseDirectories(apiKey, path)
      .then((res) => {
        setPath(res.path)
        setParent(res.parent)
        setEntries(res.directories)
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false))
  }, [apiKey, path])

  return (
    <div className="detail-overlay" onClick={onClose}>
      <div className="detail-panel directory-browser" onClick={(e) => e.stopPropagation()}>
        <div className="detail-header">
          <h2>Choose a folder</h2>
          <button className="detail-close" onClick={onClose}>
            Close
          </button>
        </div>

        <p className="directory-browser-current mono">{path || 'Select a drive'}</p>

        {error && <p className="settings-error">{error}</p>}

        <ul className="rows directory-browser-list">
          {parent !== null && (
            <li className="row row-clickable directory-browser-entry" onClick={() => setPath(parent)}>
              <span className="user-row-name">.. (up)</span>
            </li>
          )}
          {!loading && entries.length === 0 && parent === null && (
            <li className="empty">No accessible drives found.</li>
          )}
          {!loading && entries.length === 0 && parent !== null && (
            <li className="empty">No subfolders here.</li>
          )}
          {entries.map((e) => (
            <li className="row row-clickable directory-browser-entry" key={e.path} onClick={() => setPath(e.path)}>
              <span className="user-row-name">{e.name}</span>
            </li>
          ))}
        </ul>

        <div className="directory-browser-actions">
          <button onClick={onClose}>Cancel</button>
          <button disabled={!path} onClick={() => path && onSelect(path)}>
            Select this folder
          </button>
        </div>
      </div>
    </div>
  )
}

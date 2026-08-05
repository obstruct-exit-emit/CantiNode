import { useEffect, useState } from 'react'
import {
  grabRelease,
  ignoreWantedAlbum,
  listDownloads,
  listMonitoredArtists,
  listWantedAlbums,
  monitorArtist,
  searchMusicBrainzArtists,
  searchReleases,
  syncArtist,
  unmonitorArtist,
  type Download,
  type MonitoredArtist,
  type MusicBrainzArtistSearchResult,
  type ProwlarrRelease,
  type WantedAlbum,
} from '../api'

function formatBytes(bytes: number): string {
  if (!bytes) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let n = bytes
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function Wanted({ apiKey }: { apiKey: string }) {
  const [artists, setArtists] = useState<MonitoredArtist[]>([])
  const [selected, setSelected] = useState<MonitoredArtist | null>(null)
  const [monitorOpen, setMonitorOpen] = useState(false)
  const [error, setError] = useState<string | undefined>(undefined)

  function refreshArtists() {
    listMonitoredArtists(apiKey)
      .then(setArtists)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  useEffect(refreshArtists, [apiKey])

  if (error) return <p className="load-error">{error}</p>

  if (selected) {
    return (
      <ArtistWantedAlbums
        apiKey={apiKey}
        artist={selected}
        onBack={() => setSelected(null)}
        onUnmonitored={() => {
          setSelected(null)
          refreshArtists()
        }}
      />
    )
  }

  return (
    <div className="wanted">
      <div className="wanted-header">
        <button className="scan-btn" onClick={() => setMonitorOpen(true)}>
          + Monitor Artist
        </button>
      </div>

      {artists.length === 0 ? (
        <p className="empty">
          Not monitoring anyone yet. Monitor an artist to have CantiNode track their studio albums and help you find
          releases for them via Prowlarr.
        </p>
      ) : (
        <ul className="rows">
          {artists.map((a) => (
            <li className="row" key={a.id}>
              <span className="user-row-name">
                <span>{a.name}</span>
                <span className="text-muted">Monitoring since {new Date(a.added_at).toLocaleDateString()}</span>
              </span>
              <button className="toggle" onClick={() => setSelected(a)}>
                View wanted albums
              </button>
            </li>
          ))}
        </ul>
      )}

      <DownloadsActivity apiKey={apiKey} />

      {monitorOpen && (
        <MonitorArtistDialog
          apiKey={apiKey}
          onClose={() => setMonitorOpen(false)}
          onMonitored={() => {
            setMonitorOpen(false)
            refreshArtists()
          }}
        />
      )}
    </div>
  )
}

function MonitorArtistDialog({ apiKey, onClose, onMonitored }: { apiKey: string; onClose: () => void; onMonitored: () => void }) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<MusicBrainzArtistSearchResult[]>([])
  const [searching, setSearching] = useState(false)
  const [adding, setAdding] = useState<string | null>(null)
  const [error, setError] = useState<string | undefined>(undefined)

  async function handleSearch(e: React.FormEvent) {
    e.preventDefault()
    setSearching(true)
    setError(undefined)
    try {
      setResults(await searchMusicBrainzArtists(apiKey, query))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSearching(false)
    }
  }

  async function handlePick(a: MusicBrainzArtistSearchResult) {
    setAdding(a.id)
    try {
      await monitorArtist(apiKey, a.id)
      onMonitored()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setAdding(null)
    }
  }

  return (
    <div className="detail-overlay" onClick={onClose}>
      <div className="detail-panel" onClick={(e) => e.stopPropagation()}>
        <div className="detail-header">
          <h2>Monitor an artist</h2>
          <button className="detail-close" onClick={onClose}>
            ✕
          </button>
        </div>

        <form onSubmit={handleSearch} className="add-download-panel">
          <input type="text" placeholder="Artist name" value={query} onChange={(e) => setQuery(e.target.value)} autoFocus />
          <button type="submit" disabled={searching || !query.trim()}>
            {searching ? 'Searching…' : 'Search MusicBrainz'}
          </button>
        </form>

        {error && <p className="settings-error">{error}</p>}

        {results.length > 0 && (
          <ul className="rows" style={{ marginTop: 16 }}>
            {results.map((a) => (
              <li className="row" key={a.id}>
                <span className="user-row-name">{a.name}</span>
                <button disabled={adding === a.id} onClick={() => handlePick(a)}>
                  {adding === a.id ? 'Adding…' : 'Monitor'}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function ArtistWantedAlbums({
  apiKey,
  artist,
  onBack,
  onUnmonitored,
}: {
  apiKey: string
  artist: MonitoredArtist
  onBack: () => void
  onUnmonitored: () => void
}) {
  const [wanted, setWanted] = useState<WantedAlbum[]>([])
  const [error, setError] = useState<string | undefined>(undefined)
  const [searchingFor, setSearchingFor] = useState<WantedAlbum | null>(null)
  const [syncing, setSyncing] = useState(false)

  function refresh() {
    listWantedAlbums(apiKey, artist.id)
      .then(setWanted)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  useEffect(refresh, [apiKey, artist.id])

  async function handleSync() {
    setSyncing(true)
    try {
      await syncArtist(apiKey, artist.id)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setSyncing(false)
    }
  }

  async function handleUnmonitor() {
    if (!confirm(`Stop monitoring ${artist.name}? Its wanted albums and any in-flight downloads for them stop being tracked — nothing already in your library is affected.`)) return
    try {
      await unmonitorArtist(apiKey, artist.id)
      onUnmonitored()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleIgnore(w: WantedAlbum) {
    try {
      await ignoreWantedAlbum(apiKey, w.id)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="wanted">
      <nav className="breadcrumb">
        <button className="breadcrumb-link" onClick={onBack}>
          Monitored Artists
        </button>
        <span className="breadcrumb-sep">/</span>
        <span className="breadcrumb-current">{artist.name}</span>
      </nav>

      <div className="wanted-header">
        <button className="toggle" disabled={syncing} onClick={handleSync}>
          {syncing ? 'Syncing…' : 'Sync from MusicBrainz'}
        </button>
        <button className="toggle" onClick={handleUnmonitor}>
          Stop monitoring
        </button>
      </div>

      {error && <p className="load-error">{error}</p>}

      {wanted.length === 0 ? (
        <p className="empty">No albums found yet — try syncing.</p>
      ) : (
        <ul className="rows">
          {wanted.map((w) => (
            <li className="row" key={w.id}>
              <span className="user-row-name">
                <span>{w.title}</span>
                <span className="text-muted">{w.release_date ? w.release_date.slice(0, 4) : '—'}</span>
              </span>
              <span className={`badge badge-${w.status}`}>{w.status}</span>
              {w.status === 'wanted' && (
                <>
                  <button className="toggle" onClick={() => setSearchingFor(w)}>
                    Find release
                  </button>
                  <button className="toggle" onClick={() => handleIgnore(w)}>
                    Ignore
                  </button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}

      {searchingFor && (
        <ReleaseSearchDialog
          apiKey={apiKey}
          wantedAlbum={searchingFor}
          onClose={() => setSearchingFor(null)}
          onGrabbed={() => {
            setSearchingFor(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function ReleaseSearchDialog({
  apiKey,
  wantedAlbum,
  onClose,
  onGrabbed,
}: {
  apiKey: string
  wantedAlbum: WantedAlbum
  onClose: () => void
  onGrabbed: () => void
}) {
  const [releases, setReleases] = useState<ProwlarrRelease[] | null>(null)
  const [error, setError] = useState<string | undefined>(undefined)
  const [grabbing, setGrabbing] = useState<string | null>(null)

  useEffect(() => {
    searchReleases(apiKey, wantedAlbum.id)
      .then(setReleases)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }, [apiKey, wantedAlbum.id])

  async function handleGrab(rel: ProwlarrRelease) {
    setGrabbing(rel.guid)
    try {
      await grabRelease(apiKey, wantedAlbum.id, rel)
      onGrabbed()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setGrabbing(null)
    }
  }

  return (
    <div className="detail-overlay" onClick={onClose}>
      <div className="detail-panel" onClick={(e) => e.stopPropagation()}>
        <div className="detail-header">
          <h2>Releases for {wantedAlbum.title}</h2>
          <button className="detail-close" onClick={onClose}>
            ✕
          </button>
        </div>

        {error && <p className="settings-error">{error}</p>}
        {releases === null && !error && <p className="text-muted">Searching Prowlarr…</p>}
        {releases !== null && releases.length === 0 && <p className="empty">No releases found.</p>}

        {releases !== null && releases.length > 0 && (
          <ul className="rows">
            {releases.map((rel) => (
              <li className="row" key={rel.guid}>
                <span className="user-row-name">
                  <span>{rel.title}</span>
                  <span className="text-muted">
                    {rel.indexer} · {formatBytes(rel.size)} · {rel.protocol}
                    {rel.protocol === 'torrent' && rel.seeders !== undefined ? ` · ${rel.seeders} seeders` : ''}
                  </span>
                </span>
                <button disabled={grabbing === rel.guid} onClick={() => handleGrab(rel)}>
                  {grabbing === rel.guid ? 'Grabbing…' : 'Grab'}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function DownloadsActivity({ apiKey }: { apiKey: string }) {
  const [downloads, setDownloads] = useState<Download[]>([])

  useEffect(() => {
    let cancelled = false
    function poll() {
      listDownloads(apiKey)
        .then((d) => {
          if (!cancelled) setDownloads(d)
        })
        .catch(() => {
          // transient — next poll tries again
        })
    }
    poll()
    const id = setInterval(poll, 5000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [apiKey])

  if (downloads.length === 0) return null

  return (
    <div className="downloads-activity">
      <h3>Downloads</h3>
      <table className="downloads">
        <thead>
          <tr>
            <th>Title</th>
            <th>Indexer</th>
            <th>Protocol</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          {downloads.map((d) => (
            <tr key={d.id}>
              <td className="name-cell">{d.title}</td>
              <td>{d.indexer}</td>
              <td>{d.protocol}</td>
              <td>
                <span className={`badge badge-${d.status}`}>{d.status}</span>
                {d.status === 'error' && d.error_message && <div className="error-message">{d.error_message}</div>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

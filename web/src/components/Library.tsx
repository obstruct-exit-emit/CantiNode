import { useEffect, useState } from 'react'
import { listArtists, monitorArtistByMBID, searchMusicBrainzArtists, type Artist, type MusicBrainzArtistSearchResult } from '../api'
import { ArtistDetail } from './ArtistDetail'

export function Library({ apiKey }: { apiKey: string }) {
  const [artists, setArtists] = useState<Artist[]>([])
  const [selectedArtistId, setSelectedArtistId] = useState<number | null>(null)
  const [monitorOpen, setMonitorOpen] = useState(false)
  const [error, setError] = useState<string | undefined>(undefined)

  function refreshArtists() {
    listArtists(apiKey)
      .then(setArtists)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  useEffect(refreshArtists, [apiKey])

  if (error) return <p className="load-error">Couldn't load the library: {error}</p>

  if (selectedArtistId !== null) {
    return (
      <ArtistDetail
        apiKey={apiKey}
        artistId={selectedArtistId}
        onBack={() => {
          setSelectedArtistId(null)
          refreshArtists()
        }}
      />
    )
  }

  return (
    <div className="library">
      <div className="wanted-header">
        <button className="scan-btn" onClick={() => setMonitorOpen(true)}>
          + Monitor Artist
        </button>
      </div>

      {artists.length === 0 ? (
        <p className="empty">
          No artists yet. Add a root folder and run a scan to bring in matched music, or monitor an artist to start
          tracking their discography before you own anything from them.
        </p>
      ) : (
        <div className="card-grid">
          {artists.map((a) => (
            <button className="library-card" key={a.id} onClick={() => setSelectedArtistId(a.id)}>
              <div className="library-card-title">{a.name}</div>
              {a.is_monitored && <span className="badge badge-wanted">Monitoring</span>}
            </button>
          ))}
        </div>
      )}

      {monitorOpen && (
        <MonitorArtistDialog
          apiKey={apiKey}
          onClose={() => setMonitorOpen(false)}
          onMonitored={(artist) => {
            setMonitorOpen(false)
            refreshArtists()
            setSelectedArtistId(artist.id)
          }}
        />
      )}
    </div>
  )
}

function MonitorArtistDialog({
  apiKey,
  onClose,
  onMonitored,
}: {
  apiKey: string
  onClose: () => void
  onMonitored: (artist: Artist) => void
}) {
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
      const artist = await monitorArtistByMBID(apiKey, a.id)
      onMonitored(artist)
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

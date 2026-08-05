import { useEffect, useState } from 'react'
import { listUnmatched, manualMatch, searchMusicBrainz, type MusicBrainzRecording, type TrackFile } from '../api'

function tagsSummary(tagsJson: string): string {
  try {
    const t = JSON.parse(tagsJson) as { Artist?: string; Title?: string; Album?: string }
    const parts = [t.Artist, t.Title].filter(Boolean)
    return parts.length > 0 ? parts.join(' — ') : 'No tags read'
  } catch {
    return 'No tags read'
  }
}

export function Unmatched({ apiKey }: { apiKey: string }) {
  const [files, setFiles] = useState<TrackFile[]>([])
  const [error, setError] = useState<string | undefined>(undefined)
  const [matchingFor, setMatchingFor] = useState<TrackFile | null>(null)

  function refresh() {
    listUnmatched(apiKey)
      .then(setFiles)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  useEffect(refresh, [apiKey])

  if (error) return <p className="load-error">Couldn't load unmatched files: {error}</p>

  return (
    <div className="unmatched">
      {files.length === 0 ? (
        <p className="empty">Nothing waiting for review — every scanned file is matched.</p>
      ) : (
        <ul className="rows">
          {files.map((f) => (
            <li className="row" key={f.id}>
              <div className="user-row-name">
                <span className="mono">{f.path}</span>
                <span className="text-muted">{tagsSummary(f.tags_json)}</span>
              </div>
              <button className="toggle" onClick={() => setMatchingFor(f)}>
                Find match
              </button>
            </li>
          ))}
        </ul>
      )}

      {matchingFor && (
        <MatchDialog
          apiKey={apiKey}
          file={matchingFor}
          onClose={() => setMatchingFor(null)}
          onMatched={() => {
            setMatchingFor(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function MatchDialog({
  apiKey,
  file,
  onClose,
  onMatched,
}: {
  apiKey: string
  file: TrackFile
  onClose: () => void
  onMatched: () => void
}) {
  let initialTags: { Artist?: string; Title?: string; Album?: string } = {}
  try {
    initialTags = JSON.parse(file.tags_json)
  } catch {
    // no tags available — the search fields just start empty
  }

  const [artist, setArtist] = useState(initialTags.Artist ?? '')
  const [title, setTitle] = useState(initialTags.Title ?? '')
  const [album, setAlbum] = useState(initialTags.Album ?? '')
  const [results, setResults] = useState<MusicBrainzRecording[]>([])
  const [searching, setSearching] = useState(false)
  const [applying, setApplying] = useState<string | null>(null)
  const [error, setError] = useState<string | undefined>(undefined)

  async function handleSearch(e: React.FormEvent) {
    e.preventDefault()
    setSearching(true)
    setError(undefined)
    try {
      const r = await searchMusicBrainz(apiKey, { artist, album, title })
      setResults(r)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSearching(false)
    }
  }

  async function handlePick(rec: MusicBrainzRecording) {
    setApplying(rec.id)
    try {
      const release = rec.releases[0]
      await manualMatch(apiKey, file.id, rec.id, release?.id)
      onMatched()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setApplying(null)
    }
  }

  return (
    <div className="detail-overlay" onClick={onClose}>
      <div className="detail-panel" onClick={(e) => e.stopPropagation()}>
        <div className="detail-header">
          <h2>Match {file.path.split(/[/\\]/).pop()}</h2>
          <button className="detail-close" onClick={onClose}>
            ✕
          </button>
        </div>

        <form onSubmit={handleSearch} className="add-download-panel">
          <input type="text" placeholder="Artist" value={artist} onChange={(e) => setArtist(e.target.value)} />
          <input type="text" placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} />
          <input type="text" placeholder="Album" value={album} onChange={(e) => setAlbum(e.target.value)} />
          <button type="submit" disabled={searching}>
            {searching ? 'Searching…' : 'Search MusicBrainz'}
          </button>
        </form>

        {error && <p className="settings-error">{error}</p>}

        {results.length > 0 && (
          <ul className="rows" style={{ marginTop: 16 }}>
            {results.map((rec) => (
              <li className="row" key={rec.id}>
                <div className="user-row-name">
                  <span>
                    {rec['artist-credit']?.[0]?.name} — {rec.title}
                  </span>
                  <span className="text-muted">
                    {rec.releases[0]?.title ?? 'no release'} · score {rec.score}
                  </span>
                </div>
                <button disabled={applying === rec.id} onClick={() => handlePick(rec)}>
                  {applying === rec.id ? 'Applying…' : 'Use this'}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

import { Fragment, useEffect, useState } from 'react'
import {
  albumCoverURL,
  clearMatch,
  deleteTrackFile,
  listAlbumsByArtist,
  listArtists,
  listTracksByAlbum,
  listTrackFilesByTrack,
  organizeFile,
  previewOrganize,
  tagWriteSupported,
  writeTags,
  type Album,
  type Artist,
  type Track,
  type TrackFile,
} from '../api'

function formatDuration(ms: number): string {
  if (!ms) return '—'
  const totalSeconds = Math.round(ms / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${seconds.toString().padStart(2, '0')}`
}

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

export function Library({ apiKey }: { apiKey: string }) {
  const [artists, setArtists] = useState<Artist[]>([])
  const [selectedArtist, setSelectedArtist] = useState<Artist | null>(null)
  const [albums, setAlbums] = useState<Album[]>([])
  const [selectedAlbum, setSelectedAlbum] = useState<Album | null>(null)
  const [tracks, setTracks] = useState<Track[]>([])
  const [error, setError] = useState<string | undefined>(undefined)

  useEffect(() => {
    listArtists(apiKey)
      .then(setArtists)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }, [apiKey])

  function openArtist(artist: Artist) {
    setSelectedArtist(artist)
    setSelectedAlbum(null)
    setAlbums([])
    setTracks([])
    listAlbumsByArtist(apiKey, artist.id)
      .then(setAlbums)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  function openAlbum(album: Album) {
    setSelectedAlbum(album)
    setTracks([])
    listTracksByAlbum(apiKey, album.id)
      .then(setTracks)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  if (error) return <p className="load-error">Couldn't load the library: {error}</p>

  if (selectedAlbum && selectedArtist) {
    return (
      <div className="library">
        <Breadcrumb
          items={[
            { label: 'Artists', onClick: () => setSelectedArtist(null) },
            { label: selectedArtist.name, onClick: () => setSelectedAlbum(null) },
            { label: selectedAlbum.title },
          ]}
        />
        <TrackTable apiKey={apiKey} tracks={tracks} />
      </div>
    )
  }

  if (selectedArtist) {
    return (
      <div className="library">
        <Breadcrumb items={[{ label: 'Artists', onClick: () => setSelectedArtist(null) }, { label: selectedArtist.name }]} />
        {albums.length === 0 ? (
          <p className="empty">No albums yet.</p>
        ) : (
          <div className="card-grid">
            {albums.map((a) => (
              <button className="library-card" key={a.id} onClick={() => openAlbum(a)}>
                <AlbumCoverImg apiKey={apiKey} albumId={a.id} />
                <div className="library-card-title">{a.title}</div>
                <div className="library-card-sub">
                  {a.release_date ? a.release_date.slice(0, 4) : '—'} · {a.primary_type || 'Album'}
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="library">
      {artists.length === 0 ? (
        <p className="empty">
          No matched music yet. Add a root folder and run a scan — matched artists show up here once files are
          matched to MusicBrainz.
        </p>
      ) : (
        <div className="card-grid">
          {artists.map((a) => (
            <button className="library-card" key={a.id} onClick={() => openArtist(a)}>
              <div className="library-card-title">{a.name}</div>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// AlbumCoverImg hides itself entirely on error (no art cached/available
// for this release, or a fetch failure) rather than showing a browser's
// broken-image icon — the card still reads fine as title/year/type text
// only.
function AlbumCoverImg({ apiKey, albumId }: { apiKey: string; albumId: number }) {
  const [failed, setFailed] = useState(false)
  if (failed) return null
  return (
    <img
      className="library-card-cover"
      src={albumCoverURL(apiKey, albumId)}
      alt=""
      loading="lazy"
      onError={() => setFailed(true)}
    />
  )
}

function Breadcrumb({ items }: { items: { label: string; onClick?: () => void }[] }) {
  return (
    <nav className="breadcrumb">
      {items.map((item, i) => (
        <span key={i}>
          {i > 0 && <span className="breadcrumb-sep">/</span>}
          {item.onClick ? (
            <button className="breadcrumb-link" onClick={item.onClick}>
              {item.label}
            </button>
          ) : (
            <span className="breadcrumb-current">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  )
}

function TrackTable({ apiKey, tracks }: { apiKey: string; tracks: Track[] }) {
  const [expanded, setExpanded] = useState<number | null>(null)
  const [files, setFiles] = useState<TrackFile[]>([])

  function toggle(track: Track) {
    if (expanded === track.id) {
      setExpanded(null)
      return
    }
    setExpanded(track.id)
    listTrackFilesByTrack(apiKey, track.id).then(setFiles)
  }

  function refreshFiles() {
    if (expanded !== null) listTrackFilesByTrack(apiKey, expanded).then(setFiles)
  }

  if (tracks.length === 0) return <p className="empty">No tracks yet.</p>

  return (
    <table className="downloads">
      <thead>
        <tr>
          <th></th>
          <th>#</th>
          <th>Title</th>
          <th>Duration</th>
        </tr>
      </thead>
      <tbody>
        {tracks.map((t) => (
          <Fragment key={t.id}>
            <tr className="row-clickable" onClick={() => toggle(t)}>
              <td>{expanded === t.id ? '▾' : '▸'}</td>
              <td>{t.track_number || '—'}</td>
              <td>{t.title}</td>
              <td>{formatDuration(t.duration_ms)}</td>
            </tr>
            {expanded === t.id && (
              <tr>
                <td colSpan={4}>
                  <TrackFiles apiKey={apiKey} files={files} onChanged={refreshFiles} />
                </td>
              </tr>
            )}
          </Fragment>
        ))}
      </tbody>
    </table>
  )
}

function TrackFiles({ apiKey, files, onChanged }: { apiKey: string; files: TrackFile[]; onChanged: () => void }) {
  const [busy, setBusy] = useState<number | null>(null)
  const [preview, setPreview] = useState<Record<number, string>>({})

  async function handlePreview(f: TrackFile) {
    try {
      const { path } = await previewOrganize(apiKey, f.id)
      setPreview((prev) => ({ ...prev, [f.id]: path }))
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleOrganize(f: TrackFile) {
    setBusy(f.id)
    try {
      await organizeFile(apiKey, f.id)
      alert('Organized.')
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function handleWriteTags(f: TrackFile) {
    setBusy(f.id)
    try {
      await writeTags(apiKey, f.id)
      alert('Tags written.')
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function handleUnmatch(f: TrackFile) {
    if (!confirm('Unmatch this file? It moves back to the Unmatched review queue; the file itself is untouched.')) return
    setBusy(f.id)
    try {
      await clearMatch(apiKey, f.id)
      onChanged()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function handleDelete(f: TrackFile) {
    if (!confirm(`Permanently delete ${f.path}? This removes the file from disk — it cannot be undone.`)) return
    setBusy(f.id)
    try {
      await deleteTrackFile(apiKey, f.id)
      onChanged()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
      setBusy(null)
    }
  }

  if (files.length === 0) return <p className="text-muted">No file linked.</p>

  return (
    <ul className="rows">
      {files.map((f) => (
        <li className="row" key={f.id}>
          <span className="user-row-name mono">{f.path}</span>
          <span className="text-muted">
            {f.format.toUpperCase()} · {formatBytes(f.size_bytes)}
          </span>
          {preview[f.id] && <span className="text-muted mono">→ {preview[f.id]}</span>}
          <button className="toggle" onClick={() => handlePreview(f)}>
            Preview
          </button>
          <button className="toggle" disabled={busy === f.id} onClick={() => handleOrganize(f)}>
            {busy === f.id ? 'Organizing…' : 'Organize'}
          </button>
          {tagWriteSupported(f.path) && (
            <button className="toggle" disabled={busy === f.id} onClick={() => handleWriteTags(f)}>
              {busy === f.id ? 'Writing…' : 'Write tags'}
            </button>
          )}
          <button className="toggle" disabled={busy === f.id} onClick={() => handleUnmatch(f)}>
            Unmatch
          </button>
          <button className="toggle toggle-danger" disabled={busy === f.id} onClick={() => handleDelete(f)}>
            Delete
          </button>
        </li>
      ))}
    </ul>
  )
}

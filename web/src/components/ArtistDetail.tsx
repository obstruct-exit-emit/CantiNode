import { Fragment, useEffect, useState } from 'react'
import {
  albumCoverURL,
  cancelDownload,
  clearMatch,
  deleteTrackFile,
  getArtist,
  grabRelease,
  listAlbumsByArtist,
  listDownloads,
  listMissingReleaseGroups,
  listTracksByAlbum,
  listTrackFilesByTrack,
  listWantedAlbums,
  monitorArtistByID,
  organizeFile,
  previewOrganize,
  refreshArtistMetadata,
  searchReleases,
  tagWriteSupported,
  triggerScan,
  unmonitorArtist,
  wantArtistAlbum,
  writeTags,
  type Album,
  type ArtistDetail as ArtistDetailType,
  type Download,
  type ProwlarrRelease,
  type ReleaseGroupCache,
  type Track,
  type TrackFile,
  type WantedAlbum,
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

// missingBucket derives a display group from a cached release group's own
// MusicBrainz type fields — the same primary/secondary-type vocabulary
// internal/acquisition already understands (see
// musicbrainz.ReleaseGroup.isCleanAlbum's doc comment for the underlying
// convention), just bucketed a little coarser for the UI.
type MissingBucket = 'Album' | 'EP' | 'Live' | 'Compilation' | 'Other'
const BUCKET_ORDER: MissingBucket[] = ['Album', 'EP', 'Live', 'Compilation', 'Other']

function missingBucket(rg: ReleaseGroupCache): MissingBucket {
  const secondaryTypes = rg.secondary_types ?? []
  if (secondaryTypes.includes('Compilation')) return 'Compilation'
  if (secondaryTypes.includes('Live')) return 'Live'
  if (rg.primary_type === 'EP') return 'EP'
  if (rg.primary_type === 'Album' && secondaryTypes.length === 0) return 'Album'
  return 'Other'
}

function groupMissing(groups: ReleaseGroupCache[]): [MissingBucket, ReleaseGroupCache[]][] {
  const byBucket = new Map<MissingBucket, ReleaseGroupCache[]>()
  for (const rg of groups) {
    const bucket = missingBucket(rg)
    if (!byBucket.has(bucket)) byBucket.set(bucket, [])
    byBucket.get(bucket)!.push(rg)
  }
  return BUCKET_ORDER.filter((b) => byBucket.has(b)).map((b) => [b, byBucket.get(b)!])
}

export function ArtistDetail({ apiKey, artistId, onBack }: { apiKey: string; artistId: number; onBack: () => void }) {
  const [detail, setDetail] = useState<ArtistDetailType | null>(null)
  const [albums, setAlbums] = useState<Album[]>([])
  const [missing, setMissing] = useState<ReleaseGroupCache[]>([])
  const [wanted, setWanted] = useState<WantedAlbum[]>([])
  const [selectedAlbum, setSelectedAlbum] = useState<Album | null>(null)
  const [tracks, setTracks] = useState<Track[]>([])
  const [error, setError] = useState<string | undefined>(undefined)
  const [monitorBusy, setMonitorBusy] = useState(false)
  const [refreshBusy, setRefreshBusy] = useState(false)
  const [scanBusy, setScanBusy] = useState(false)
  const [addBusy, setAddBusy] = useState<string | null>(null)
  const [searchingFor, setSearchingFor] = useState<WantedAlbum | null>(null)

  function refresh() {
    getArtist(apiKey, artistId)
      .then(setDetail)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
    listAlbumsByArtist(apiKey, artistId).then(setAlbums).catch(() => {})
    listMissingReleaseGroups(apiKey, artistId).then(setMissing).catch(() => {})
    listWantedAlbums(apiKey, artistId).then(setWanted).catch(() => {})
  }

  useEffect(refresh, [apiKey, artistId])

  async function handleMonitorToggle() {
    if (!detail) return
    setMonitorBusy(true)
    try {
      if (detail.is_monitored) {
        await unmonitorArtist(apiKey, artistId)
      } else {
        await monitorArtistByID(apiKey, artistId)
      }
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setMonitorBusy(false)
    }
  }

  async function handleRefreshMetadata() {
    setRefreshBusy(true)
    try {
      await refreshArtistMetadata(apiKey, artistId)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setRefreshBusy(false)
    }
  }

  // No per-artist scan exists on the backend (a scan walks every root
  // folder looking for matches, it isn't scoped to one artist) — this
  // triggers the same global scan Library's header would, which is the
  // closest equivalent to "check for new files for this artist" v1 has.
  async function handleScan() {
    setScanBusy(true)
    try {
      await triggerScan(apiKey)
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setScanBusy(false)
    }
  }

  async function handleAdd(rg: ReleaseGroupCache, monitor: boolean) {
    setAddBusy(rg.release_group_mbid)
    try {
      await wantArtistAlbum(apiKey, artistId, rg.release_group_mbid, monitor)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setAddBusy(null)
    }
  }

  async function handleAddAll(groups: ReleaseGroupCache[], monitor: boolean) {
    setAddBusy(groups[0]?.release_group_mbid ?? 'bulk')
    try {
      for (const rg of groups) {
        await wantArtistAlbum(apiKey, artistId, rg.release_group_mbid, monitor)
      }
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setAddBusy(null)
    }
  }

  function openAlbum(album: Album) {
    setSelectedAlbum(album)
    setTracks([])
    listTracksByAlbum(apiKey, album.id)
      .then(setTracks)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }

  if (error) return <p className="load-error">Couldn't load this artist: {error}</p>
  if (!detail) return null

  if (selectedAlbum) {
    return (
      <div className="library">
        <nav className="breadcrumb">
          <button className="breadcrumb-link" onClick={onBack}>
            Artists
          </button>
          <span className="breadcrumb-sep">/</span>
          <button className="breadcrumb-link" onClick={() => setSelectedAlbum(null)}>
            {detail.name}
          </button>
          <span className="breadcrumb-sep">/</span>
          <span className="breadcrumb-current">{selectedAlbum.title}</span>
        </nav>
        <TrackTable apiKey={apiKey} tracks={tracks} />
      </div>
    )
  }

  const wantedIDsForArtist = new Set(wanted.map((w) => w.id))
  // "downloaded" means it's already an owned album, shown above — listing
  // it again here would just be a redundant, actionless entry. The
  // underlying wantedIDsForArtist set above keeps every status (including
  // downloaded) so its download history still shows in ArtistDownloads.
  const visibleWanted = wanted.filter((w) => w.status !== 'downloaded')

  return (
    <div className="artist-detail">
      <button className="breadcrumb-link" onClick={onBack}>
        ← Artists
      </button>

      <div className="artist-header">
        {detail.image_url && <img className="artist-header-image" src={detail.image_url} alt="" />}
        <div className="artist-header-info">
          <h2>{detail.name}</h2>
          <p className="text-muted">
            {detail.owned_album_count} owned · {detail.is_monitored ? 'Monitoring' : 'Not monitored'}
          </p>
          {detail.bio && <p className="artist-bio">{detail.bio}</p>}
          <div className="artist-header-actions">
            <button className="toggle" disabled={monitorBusy} onClick={handleMonitorToggle}>
              {monitorBusy ? 'Working…' : detail.is_monitored ? 'Unmonitor' : 'Monitor'}
            </button>
            <button className="toggle" disabled={refreshBusy} onClick={handleRefreshMetadata}>
              {refreshBusy ? 'Refreshing…' : 'Refresh metadata'}
            </button>
            <button className="toggle" disabled={scanBusy} onClick={handleScan}>
              {scanBusy ? 'Scanning…' : 'Scan files'}
            </button>
          </div>
        </div>
      </div>

      <section>
        <h3 className="section-title">Albums</h3>
        {albums.length === 0 ? (
          <p className="empty">No owned albums yet.</p>
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
      </section>

      <section>
        <h3 className="section-title">Missing</h3>
        {missing.length === 0 ? (
          <p className="empty">Nothing missing — monitor or refresh this artist to check for new releases.</p>
        ) : (
          groupMissing(missing).map(([bucket, groups]) => (
            <div className="missing-group" key={bucket}>
              <div className="missing-group-header">
                <span>
                  {bucket} <span className="text-muted">({groups.length})</span>
                </span>
                <span className="missing-group-actions">
                  <button className="toggle" disabled={addBusy !== null} onClick={() => handleAddAll(groups, false)}>
                    + Add all ({groups.length})
                  </button>
                  <button className="toggle" disabled={addBusy !== null} onClick={() => handleAddAll(groups, true)}>
                    + Add & Monitor all ({groups.length})
                  </button>
                </span>
              </div>
              <ul className="rows">
                {groups.map((rg) => (
                  <li className="row" key={rg.release_group_mbid}>
                    <span className="user-row-name">
                      <span>{rg.title}</span>
                      <span className="text-muted">{rg.first_release_date ? rg.first_release_date.slice(0, 4) : '—'}</span>
                    </span>
                    <button className="toggle" disabled={addBusy === rg.release_group_mbid} onClick={() => handleAdd(rg, false)}>
                      + Add
                    </button>
                    <button className="toggle" disabled={addBusy === rg.release_group_mbid} onClick={() => handleAdd(rg, true)}>
                      + Add & Monitor
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))
        )}
      </section>

      {visibleWanted.length > 0 && (
        <section>
          <h3 className="section-title">Wanted</h3>
          <ul className="rows">
            {visibleWanted.map((w) => (
              <li className="row" key={w.id}>
                <span className="user-row-name">
                  <span>{w.title}</span>
                  <span className="text-muted">{w.release_date ? w.release_date.slice(0, 4) : '—'}</span>
                </span>
                <span className={`badge badge-${w.status}`}>{w.status}</span>
                {w.status === 'wanted' && (
                  <button className="toggle" onClick={() => setSearchingFor(w)}>
                    Find release
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <ArtistDownloads apiKey={apiKey} wantedIDs={wantedIDsForArtist} />

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

// isDeadTorrent flags a torrent release with no seeders — grabbing it
// would just sit at 0% forever, so these sink to the bottom of the list
// instead of competing with releases that'll actually download.
function isDeadTorrent(rel: ProwlarrRelease): boolean {
  return rel.protocol === 'torrent' && (rel.seeders ?? 0) === 0
}

// sortReleases pushes dead torrents to the bottom; everything else keeps
// Prowlarr's own relative order (JS's sort is stable).
function sortReleases(releases: ProwlarrRelease[]): ProwlarrRelease[] {
  return releases
    .map((rel, index) => ({ rel, index }))
    .sort((a, b) => {
      const deadDiff = Number(isDeadTorrent(a.rel)) - Number(isDeadTorrent(b.rel))
      return deadDiff !== 0 ? deadDiff : a.index - b.index
    })
    .map(({ rel }) => rel)
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
      .then((r) => setReleases(sortReleases(r)))
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
            {releases.map((rel) => {
              const dead = isDeadTorrent(rel)
              return (
                <li className="row" key={rel.guid}>
                  <span className="user-row-name">
                    <span>{rel.title}</span>
                    <span className="text-muted">
                      {rel.indexer} · {formatBytes(rel.size)} · {rel.protocol}
                      {rel.protocol === 'torrent' && (
                        <span className={dead ? 'release-dead' : undefined}>
                          {' · '}
                          {rel.seeders ?? 0} seeders · {rel.leechers ?? 0} peers
                          {dead ? ' (dead)' : ''}
                        </span>
                      )}
                    </span>
                  </span>
                  <button disabled={grabbing === rel.guid || dead} onClick={() => handleGrab(rel)}>
                    {grabbing === rel.guid ? 'Grabbing…' : dead ? 'No seeders' : 'Grab'}
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
  )
}

// ArtistDownloads shows only this artist's own in-flight/recent
// downloads — filtered client-side from the global downloads list by
// wantedIDs, since Download only carries a wanted_album_id, not an
// artist_id, and adding a dedicated per-artist downloads endpoint felt
// like overkill for what's already a small, cheap list to filter here.
function ArtistDownloads({ apiKey, wantedIDs }: { apiKey: string; wantedIDs: Set<number> }) {
  const [downloads, setDownloads] = useState<Download[]>([])
  const [canceling, setCanceling] = useState<number | null>(null)

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

  async function handleCancel(d: Download) {
    if (!confirm(`Cancel "${d.title}"? It's removed from its download client and the album goes back to wanted.`)) return
    setCanceling(d.id)
    try {
      await cancelDownload(apiKey, d.id)
      setDownloads(await listDownloads(apiKey))
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    } finally {
      setCanceling(null)
    }
  }

  const mine = downloads.filter((d) => wantedIDs.has(d.wanted_album_id))
  if (mine.length === 0) return null

  return (
    <section className="downloads-activity">
      <h3 className="section-title">Downloads</h3>
      <table className="downloads">
        <thead>
          <tr>
            <th>Title</th>
            <th>Indexer</th>
            <th>Protocol</th>
            <th>Status</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {mine.map((d) => (
            <tr key={d.id}>
              <td className="name-cell">{d.title}</td>
              <td>{d.indexer}</td>
              <td>{d.protocol}</td>
              <td>
                <span className={`badge badge-${d.status}`}>{d.status}</span>
                {d.status === 'error' && d.error_message && <div className="error-message">{d.error_message}</div>}
              </td>
              <td>
                {(d.status === 'downloading' || d.status === 'error') && (
                  <button className="toggle" disabled={canceling === d.id} onClick={() => handleCancel(d)}>
                    {canceling === d.id ? 'Canceling…' : 'Cancel'}
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}

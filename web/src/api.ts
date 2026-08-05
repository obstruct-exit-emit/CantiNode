// Thin client for CantiNode's native /api/v1. No generated client, no
// fetch-wrapper library — this is small enough to just write directly.

const API_KEY_STORAGE_KEY = 'cantinode_api_key'

export function loadStoredApiKey(): string {
  return localStorage.getItem(API_KEY_STORAGE_KEY) ?? ''
}

export function storeApiKey(key: string) {
  localStorage.setItem(API_KEY_STORAGE_KEY, key)
}

export function clearStoredApiKey() {
  localStorage.removeItem(API_KEY_STORAGE_KEY)
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, apiKey: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    ...init,
    headers: { ...init?.headers, Authorization: `Bearer ${apiKey}` },
  })
  if (!resp.ok) {
    const text = await resp.text().catch(() => '')
    throw new ApiError(resp.status, text || `${resp.status} ${resp.statusText}`)
  }
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

function json(body: unknown): RequestInit {
  return { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

export function getVersion(apiKey: string): Promise<{ version: string }> {
  return request('/api/v1/version', apiKey)
}

export interface RootFolder {
  id: number
  path: string
  created_at: string
}

export function listRootFolders(apiKey: string): Promise<RootFolder[]> {
  return request('/api/v1/root-folders', apiKey)
}

export function createRootFolder(apiKey: string, path: string): Promise<RootFolder> {
  return request('/api/v1/root-folders', apiKey, { method: 'POST', ...json({ path }) })
}

export function deleteRootFolder(apiKey: string, id: number): Promise<void> {
  return request(`/api/v1/root-folders/${id}`, apiKey, { method: 'DELETE' })
}

export interface BrowseEntry {
  name: string
  path: string
}

export interface BrowseDirectoriesResult {
  path: string
  parent: string | null
  directories: BrowseEntry[]
}

// browseDirectories lists the subdirectories of path (server-side, since
// CantiNode organizes files on the machine it runs on, not the browser's
// own filesystem) — an empty path lists top-level roots instead (drive
// letters on Windows, "/" elsewhere), for the root-folder picker.
export function browseDirectories(apiKey: string, path: string): Promise<BrowseDirectoriesResult> {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return request(`/api/v1/browse-directories${q}`, apiKey)
}

export interface Artist {
  id: number
  mbid: string
  name: string
  sort_name: string
}

export interface Album {
  id: number
  artist_id: number
  mbid: string
  release_group_mbid: string
  title: string
  release_date: string
  primary_type: string
}

export interface Track {
  id: number
  album_id: number
  mbid: string
  title: string
  track_number: number
  disc_number: number
  duration_ms: number
}

export type MatchStatus = 'unmatched' | 'matched' | 'manual'

export interface TrackFile {
  id: number
  root_folder_id: number
  track_id: number | null
  path: string
  size_bytes: number
  format: string
  bitrate_kbps: number
  duration_ms: number
  tags_json: string
  match_status: MatchStatus
  match_confidence: number
  scanned_at: string
  organized_at?: string
}

// albumCoverURL builds the <img src> for an album's cover art. Uses the
// ?api_key= query-param auth path (see internal/api's
// requireAuthHeaderOrQuery) since a plain <img> tag can't send an
// Authorization header.
export function albumCoverURL(apiKey: string, albumId: number): string {
  return `/api/v1/albums/${albumId}/cover?api_key=${encodeURIComponent(apiKey)}`
}

export function listArtists(apiKey: string): Promise<Artist[]> {
  return request('/api/v1/artists', apiKey)
}

export function listAlbumsByArtist(apiKey: string, artistId: number): Promise<Album[]> {
  return request(`/api/v1/artists/${artistId}/albums`, apiKey)
}

export function listTracksByAlbum(apiKey: string, albumId: number): Promise<Track[]> {
  return request(`/api/v1/albums/${albumId}/tracks`, apiKey)
}

export function listTrackFilesByTrack(apiKey: string, trackId: number): Promise<TrackFile[]> {
  return request(`/api/v1/tracks/${trackId}/files`, apiKey)
}

export function listUnmatched(apiKey: string): Promise<TrackFile[]> {
  return request('/api/v1/track-files/unmatched', apiKey)
}

export function clearMatch(apiKey: string, trackFileId: number): Promise<void> {
  return request(`/api/v1/track-files/${trackFileId}/match`, apiKey, { method: 'DELETE' })
}

export function manualMatch(apiKey: string, trackFileId: number, recordingMbid: string, releaseMbid?: string): Promise<TrackFile> {
  return request(`/api/v1/track-files/${trackFileId}/match`, apiKey, {
    method: 'POST',
    ...json({ recording_mbid: recordingMbid, release_mbid: releaseMbid ?? '' }),
  })
}

export function previewOrganize(apiKey: string, trackFileId: number): Promise<{ path: string }> {
  return request(`/api/v1/track-files/${trackFileId}/organize/preview`, apiKey)
}

export function organizeFile(apiKey: string, trackFileId: number): Promise<{ path: string }> {
  return request(`/api/v1/track-files/${trackFileId}/organize`, apiKey, { method: 'POST' })
}

// writeTags embeds the file's matched metadata (artist/album/track,
// MusicBrainz IDs) back into its own tags — MP3/FLAC only, see
// internal/tagwriter's doc comment for why other formats aren't
// supported yet.
export function writeTags(apiKey: string, trackFileId: number): Promise<void> {
  return request(`/api/v1/track-files/${trackFileId}/write-tags`, apiKey, { method: 'POST' })
}

// tagWriteSupported mirrors internal/tagwriter.IsSupported — used to
// decide whether to show the "Write tags" action at all for a given
// file, rather than letting the user hit an error after the fact.
export function tagWriteSupported(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase()
  return ext === 'mp3' || ext === 'flac'
}

export interface ArtistRef {
  id: string
  name: string
  'sort-name': string
}

export interface ArtistCredit {
  name: string
  artist: ArtistRef
}

export interface ReleaseGroupRef {
  id: string
  title: string
  'primary-type': string
}

export interface ReleaseRef {
  id: string
  title: string
  date: string
  'release-group': ReleaseGroupRef
}

export interface MusicBrainzRecording {
  id: string
  title: string
  length: number
  score: number
  'artist-credit': ArtistCredit[]
  releases: ReleaseRef[]
}

export function searchMusicBrainz(apiKey: string, params: { artist?: string; album?: string; title?: string }): Promise<MusicBrainzRecording[]> {
  const q = new URLSearchParams()
  if (params.artist) q.set('artist', params.artist)
  if (params.album) q.set('album', params.album)
  if (params.title) q.set('title', params.title)
  return request(`/api/v1/musicbrainz/search?${q.toString()}`, apiKey)
}

export interface ScanResult {
  FilesFound: number
  FilesMatched: number
  FilesOrganized: number
  FilesRemoved: number
  Errors: string[]
}

export interface ScanState {
  running: boolean
  started_at?: string
  finished_at?: string
  result?: ScanResult
  error?: string
}

export function triggerScan(apiKey: string): Promise<{ status: string }> {
  return request('/api/v1/scan', apiKey, { method: 'POST' })
}

export function getScanStatus(apiKey: string): Promise<ScanState> {
  return request('/api/v1/scan/status', apiKey)
}

export interface Settings {
  api_key: string
  port: number
  log_level: string
  scan_interval_hours: number
  naming_format: string
  organize_on_match: boolean
  min_match_confidence: number
  musicbrainz_contact_email: string
  prowlarr_url: string
  prowlarr_api_key: string
  qbittorrent_url: string
  qbittorrent_username: string
  qbittorrent_password: string
  sabnzbd_url: string
  sabnzbd_api_key: string
}

export function getSettings(apiKey: string): Promise<Settings> {
  return request('/api/v1/settings', apiKey)
}

export function updateSettings(apiKey: string, settings: Settings): Promise<Settings> {
  return request('/api/v1/settings', apiKey, { method: 'PUT', ...json(settings) })
}

// --- Acquisition: monitor an artist, want their albums, search Prowlarr,
// grab via qBittorrent or SABnzbd. Optional — Prowlarr and the download
// clients may not be configured yet, in which case search/grab calls
// fail with a plain error message from the backend (see
// internal/acquisition). ---

export interface MusicBrainzArtistSearchResult {
  id: string
  name: string
  'sort-name': string
  score: number
}

export function searchMusicBrainzArtists(apiKey: string, query: string): Promise<MusicBrainzArtistSearchResult[]> {
  return request(`/api/v1/musicbrainz/artist-search?query=${encodeURIComponent(query)}`, apiKey)
}

export interface MonitoredArtist {
  id: number
  mbid: string
  name: string
  sort_name: string
  added_at: string
  last_synced_at?: string
}

export function listMonitoredArtists(apiKey: string): Promise<MonitoredArtist[]> {
  return request('/api/v1/monitored-artists', apiKey)
}

export function monitorArtist(apiKey: string, mbid: string): Promise<MonitoredArtist> {
  return request('/api/v1/monitored-artists', apiKey, { method: 'POST', ...json({ mbid }) })
}

export function unmonitorArtist(apiKey: string, id: number): Promise<void> {
  return request(`/api/v1/monitored-artists/${id}`, apiKey, { method: 'DELETE' })
}

export function syncArtist(apiKey: string, id: number): Promise<void> {
  return request(`/api/v1/monitored-artists/${id}/sync`, apiKey, { method: 'POST' })
}

export type WantedStatus = 'wanted' | 'downloading' | 'downloaded' | 'ignored'

export interface WantedAlbum {
  id: number
  monitored_artist_id: number
  release_group_mbid: string
  title: string
  primary_type: string
  release_date: string
  status: WantedStatus
  added_at: string
}

export function listWantedAlbums(apiKey: string, monitoredArtistId: number): Promise<WantedAlbum[]> {
  return request(`/api/v1/monitored-artists/${monitoredArtistId}/wanted`, apiKey)
}

export function ignoreWantedAlbum(apiKey: string, id: number): Promise<void> {
  return request(`/api/v1/wanted-albums/${id}/ignore`, apiKey, { method: 'POST' })
}

export interface ProwlarrRelease {
  guid: string
  title: string
  size: number
  indexerId: number
  indexer: string
  publishDate: string
  downloadUrl?: string
  magnetUrl?: string
  infoUrl?: string
  protocol: 'torrent' | 'usenet' | 'unknown'
  seeders?: number
  leechers?: number
}

export function searchReleases(apiKey: string, wantedAlbumId: number): Promise<ProwlarrRelease[]> {
  return request(`/api/v1/wanted-albums/${wantedAlbumId}/search`, apiKey)
}

export function grabRelease(apiKey: string, wantedAlbumId: number, release: ProwlarrRelease): Promise<Download> {
  return request(`/api/v1/wanted-albums/${wantedAlbumId}/grab`, apiKey, { method: 'POST', ...json(release) })
}

export type DownloadStatus = 'downloading' | 'completed' | 'imported' | 'error'

export interface Download {
  id: number
  wanted_album_id: number
  root_folder_id: number
  protocol: 'torrent' | 'usenet'
  client_id: string
  title: string
  indexer: string
  status: DownloadStatus
  error_message: string
  grabbed_at: string
  completed_at?: string
  imported_at?: string
}

export function listDownloads(apiKey: string): Promise<Download[]> {
  return request('/api/v1/downloads', apiKey)
}

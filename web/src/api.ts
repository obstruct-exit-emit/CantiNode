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
}

export function getSettings(apiKey: string): Promise<Settings> {
  return request('/api/v1/settings', apiKey)
}

export function updateSettings(apiKey: string, settings: Settings): Promise<Settings> {
  return request('/api/v1/settings', apiKey, { method: 'PUT', ...json(settings) })
}

// Typed client for CantiNode's REST API. The API key is kept in localStorage
// for now; proper session handling comes with the settings UI.

export interface SystemStatus {
  appName: string;
  appVersion: string;
  os: string;
  arch: string;
  uptime: string;
  dataDir: string;
  startTime: string;
  ipAddresses: string[];
  port: number;
}

export interface RootFolder {
  id: number;
  name: string;
  mediaType: string;
  path: string;
  isDefault: boolean;
  accessible: boolean;
}

export interface Indexer {
  id: number;
  name: string;
  // "newznab" | "torznab" | a native implementation name.
  type: string;
  baseUrl: string;
  apiKey: string;
  audioCategories: string;
  enabled: boolean;
  priority: number;
  addedAt?: string;
}

// NativeIndexer describes a built-in scraped/bespoke source selectable as an
// indexer type — no Newznab/Torznab URL, just the implementation.
export interface NativeIndexer {
  name: string;
  displayName: string;
  protocol: string;
  mediaTypes: string[];
  defaultBaseUrl: string;
  needsBaseUrl: boolean;
  needsApiKey: boolean;
  // Experimental scraped source — the UI shows a work-in-progress warning.
  wip: boolean;
}

export interface DownloadClient {
  id: number;
  name: string;
  type: "qbittorrent" | "sabnzbd" | "direct";
  host: string;
  username: string;
  password: string;
  apiKey: string;
  category: string;
  enabled: boolean;
  priority: number;
}

export interface QueueItem {
  client: string;
  clientConfigId: number;
  id: string;
  title: string;
  status: string;
  progress: number;
  path?: string;
  // Set when the item belongs to a tracked grab — links it to its wanted album.
  grabId?: number;
  wantedAlbumId?: number;
  mediaType?: string;
}

export interface Release {
  indexerId: number;
  indexer: string;
  protocol: "usenet" | "torrent" | "direct";
  title: string;
  guid: string;
  infoUrl?: string;
  downloadUrl: string;
  size: number;
  publishDate?: string;
  seeders: number;
  peers: number;
}

// Parsed is what internal/release.Parse read out of a release's own title —
// best-effort, zero values mean "not stated". Music release titles can
// carry all of these (a bitrate token or a "Complete Discography"-style
// pack declaration are as real for music as anywhere else); narrator and
// abridged are audiobook-only concepts that never populate for music.
export interface ParsedRelease {
  formats?: string[];
  language?: string;
  retail: boolean;
  year?: number;
  group?: string;
  bitrate?: number;
  volume?: number;
  volumeEnd?: number;
  pack?: boolean;
}

// ReleaseCandidate is one release scored against the active quality
// profile — internal/release.Candidate embeds Release flat, so this does
// too. approved false always comes with at least one rejection reason.
export interface ReleaseCandidate extends Release {
  parsed: ParsedRelease;
  score: number;
  approved: boolean;
  rejections?: string[];
}

export interface GrabRecord {
  id: number;
  wantedAlbumId?: number;
  upgradeAlbumId?: number;
  title: string;
  protocol: string;
  status: "grabbed" | "imported" | "failed";
  message?: string;
  grabbedAt: string;
  completedAt?: string;
}

export interface AuthStatus {
  authEnabled: boolean;
  authenticated: boolean;
  // Present once signed in — absent when auth is disabled or not yet
  // authenticated (API-key access, no per-account identity).
  username?: string;
  role?: "admin" | "member";
}

export interface BackupInfo {
  name: string;
  size: number;
  createdAt: string;
}

export interface HealthIssue {
  source: string;
  level: "error" | "warning";
  message: string;
}

export interface HealthResult {
  issues: HealthIssue[];
  checkedAt: string; // zero time before the first background run
}

export interface ImportResult {
  imported: number;
  failed: number;
  skipped: number;
  messages?: string[];
}

export interface QualityProfile {
  id: number;
  name: string;
  mediaType: string;
  formats: string[];
  language: string;
  retailBonus: number;
  minSize: number;
  maxSize: number;
  upgradesAllowed: boolean;
  cutoff?: string;
  isDefault: boolean;
}

export interface BlockEntry {
  id: number;
  guid?: string;
  title: string;
  reason?: string;
  blockedAt: string;
}

export interface NamingSettings {
  musicFile: string;
  musicExample: string;
  disableDiscNumberForSingleDisc: boolean;
}

export interface RenameMove {
  fileId: number;
  from: string;
  to: string;
}

export interface ArtistMove {
  fileId: number;
  from: string;
  to: string;
  sizeBytes: number;
}

export interface MusicMoveState {
  running: boolean;
  artistId?: number;
  artistName?: string;
  destRootFolderId?: number;
  startedAt?: string;
  finishedAt?: string;
  moved?: ArtistMove[];
  errors?: string[];
  error?: string;
}

// PathMapping translates a download client's reported path prefix into the
// path where CantiNode sees the same files (remote client / container
// setups). Longest matching prefix wins.
export interface PathMapping {
  remotePrefix: string;
  localPrefix: string;
}

// TimingSettings: background loop cadences; 0/"" = use the built-in
// default. Changes apply on the next server start. Scan/organize/manual
// search stay user-triggered; the wanted-list sweep (internal/autosearch)
// and health check are the two background loops these cadences tune.
//
// wantedSearchMode picks how the sweep is scheduled: "daily" (the
// default — once a day at wantedSearchTimeOfDay) or "interval" (every
// wantedSearchIntervalMinutes). The two fields for the mode not in use are
// simply ignored server-side, not validated away — no need to blank them
// out when switching modes in the UI.
export interface TimingSettings {
  healthIntervalMinutes: number;
  wantedSearchMode: "" | "daily" | "interval";
  wantedSearchIntervalMinutes: number;
  wantedSearchTimeOfDay: string; // "HH:MM", 24-hour, server-local time
  discographyRefreshIntervalMinutes: number;
}

// UserAccount is one login; the default user is protected from removal.
export interface UserAccount {
  username: string;
  default: boolean;
  role: "admin" | "member";
}

// FolderListing is one level of the server's filesystem for the folder picker.
export interface FolderListing {
  path: string;
  parent: string;
  directories: { name: string; path: string }[];
}

// --- Music: the only media-type domain — see internal/musiclibrary's
// package doc comment on the Go side. ---

export interface MusicArtist {
  id: number;
  mbid: string;
  name: string;
  sortName: string;
  isMonitored: boolean;
  lastSyncedAt?: string;
  bio: string;
  imageUrl: string;
  metadataFetchedAt?: string;
  createdAt: string;
  updatedAt: string;
  ownedAlbumCount?: number;
  totalAlbumCount?: number;
  // "artist" (the default) for a real MusicBrainz artist, "series" for a
  // MusicBrainz Series tracked as a synthetic library artist (see
  // addMusicSeries) — otherwise behaves identically everywhere in the UI.
  kind: string;
}

export interface MusicAlbum {
  id: number;
  artistId: number;
  mbid: string;
  releaseGroupMbid: string;
  title: string;
  releaseDate: string;
  primaryType: string;
  description: string;
  mood: string;
  // Absent (not merely falsy) until the description has actually been
  // looked up once — see internal/musiclibrary.Album's own doc comment.
  // Its presence, not description's own emptiness, is what tells the
  // album page whether it still needs to call getMusicAlbumDescription.
  descriptionFetchedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface MusicTrack {
  id: number;
  albumId: number;
  mbid: string;
  title: string;
  trackNumber: number;
  discNumber: number;
  durationMs: number;
  createdAt: string;
  updatedAt: string;
  // This track's own real performing-artist credit from MusicBrainz —
  // only present when it differs from the album's own artist (e.g. a
  // "Various Artists" compilation's individual track performers).
  artistCredit?: string;
  // Only populated by the album track list (listMusicTracks) — true when
  // this track already appears in some playlist.
  inPlaylist?: boolean;
}

export interface MusicTrackFile {
  id: number;
  rootFolderId: number;
  trackId?: number;
  path: string;
  sizeBytes: number;
  format: string;
  bitrateKbps: number;
  durationMs: number;
  tagsJson: string;
  matchStatus: "unmatched" | "matched" | "manual";
  matchConfidence: number;
  scannedAt: string;
  organizedAt?: string;
}

// UnmatchedTrackFile is MusicTrackFile plus the folder "group key" the
// server computed for it — see listUnmatchedTrackFiles. groupPath is the
// same folder with its own root folder's path stripped off the front —
// display-only, so the review page can show where a file lives without
// its full on-disk path; empty when the file sits directly in its root
// folder.
export interface UnmatchedTrackFile extends MusicTrackFile {
  groupKey: string;
  groupPath: string;
}

// MusicTrackFileTags is a track file's own embedded tags, read live off
// disk (not MusicTrackFile.tagsJson's snapshot from its last scan, which
// goes stale after a "Write tags" call) — see getMusicTrackFileTags.
export interface MusicTrackFileTags {
  title: string;
  artist: string;
  albumArtist: string;
  album: string;
  trackNumber: number;
  discNumber: number;
  year: number;
  format: string;
  musicBrainzArtistId: string;
  albumArtistId?: string;
  musicBrainzAlbumId: string;
  musicBrainzReleaseGroupId: string;
  musicBrainzRecordingId: string;
  genre?: string;
  releaseType?: string;
  artistSortName?: string;
  albumArtistSortName?: string;
  trackTotal?: number;
  discTotal?: number;
  releaseCountry?: string;
  releaseStatus?: string;
  media?: string;
  composer?: string;
}

// TrackSuggestion is one proposed track_file → recording slot from
// suggestTrackFileMatches — a proposal only, nothing commits until it's
// sent through matchTrackFile like any other match.
export interface TrackSuggestion {
  trackFileId: number;
  recordingMbid: string;
  releaseMbid: string;
  trackTitle: string;
  trackNumber: number;
  discNumber: number;
}

export interface Playlist {
  id: number;
  name: string;
  description: string;
  trackCount: number;
  totalDurationMs: number;
  createdAt: string;
  updatedAt: string;
}

// PlaylistTrack is one playlist_items row joined out to what's needed to
// show/use it — see internal/musiclibrary's own doc comment on the Go
// struct this mirrors. artistId/artistName are the album's artist (the
// navigable one, same convention AlbumDetailView already uses);
// artistCredit is a supplementary "featuring" credit only.
export interface PlaylistTrack {
  itemId: number;
  trackId: number;
  position: number;
  title: string;
  durationMs: number;
  artistCredit?: string;
  artistId: number;
  artistName: string;
  albumId: number;
  albumTitle: string;
  // Absent when nothing currently backs this track (deleted, never
  // matched) — still a real entry, just not exportable until it's owned
  // again.
  trackFileId?: number;
  path?: string;
}

export interface PlaylistDetail extends Playlist {
  tracks: PlaylistTrack[];
}

// TrackSearchResult is one owned, file-backed track matching a title
// search — the Search page's track-level results.
export interface TrackSearchResult {
  trackId: number;
  title: string;
  durationMs: number;
  artistCredit?: string;
  artistId: number;
  artistName: string;
  albumId: number;
  albumTitle: string;
  trackFileId: number;
}

export interface MusicReleaseGroup {
  id: number;
  artistId: number;
  releaseGroupMbid: string;
  title: string;
  primaryType: string;
  secondaryTypes: string[];
  firstReleaseDate: string;
}

// CalendarEntry is one not-yet-owned release from a monitored artist,
// falling inside the release Calendar's date window.
export interface CalendarEntry {
  artistId: number;
  artistName: string;
  releaseGroupMbid: string;
  title: string;
  primaryType: string;
  secondaryTypes: string[];
  firstReleaseDate: string;
  wantedAlbumId?: number;
  wantedStatus?: string;
}

// ReleaseGroupTrack/ReleaseGroupTracklist preview a release group's tracks
// straight from MusicBrainz — used by the Missing section, where there's no
// local album/track row yet (nothing owned, nothing wanted-and-matched).
export interface ReleaseGroupTrack {
  discNumber: number;
  position: number;
  title: string;
  durationMs: number;
  recordingMbid: string;
}

export interface ReleaseGroupTracklist {
  releaseMbid: string;
  releaseTitle: string;
  tracks: ReleaseGroupTrack[];
}

// ReleaseGroupVersion is one known release (pressing/edition) of a release
// group, cached so the matching UI can offer a version picker without a
// live MusicBrainz call — see listReleaseGroupVersions.
export interface ReleaseGroupVersion {
  id: number;
  releaseGroupMbid: string;
  releaseMbid: string;
  title: string;
  releaseDate: string;
  country: string;
  status: string;
  disambiguation: string;
  trackCount: number;
  mediaSummary: string;
  isRepresentative: boolean;
  fetchedAt: string;
}

export interface WantedAlbum {
  id: number;
  artistId: number;
  releaseGroupMbid: string;
  title: string;
  primaryType: string;
  releaseDate: string;
  status: "wanted" | "downloading";
  addedAt: string;
}

export interface MusicBrainzArtistResult {
  id: string;
  name: string;
  sortName: string;
  type?: string;
  disambiguation?: string;
  country?: string;
  score: number;
}

export interface MusicBrainzRecordingResult {
  id: string;
  title: string;
  length: number;
  score: number;
}

export interface MusicScanResult {
  filesFound: number;
  filesMatched: number;
  filesOrganized: number;
  filesRemoved: number;
  errors?: string[];
}

export interface MusicScanState {
  running: boolean;
  startedAt?: string;
  finishedAt?: string;
  result?: MusicScanResult;
  error?: string;
}

export interface ImportResult {
  Checked: number;
  Imported: number;
  Failed: number;
}

export interface ImportState {
  running: boolean;
  startedAt?: string;
  finishedAt?: string;
  result?: ImportResult;
}

export interface MusicSettings {
  organizeOnMatch: boolean;
  minMatchConfidence: number;
  autoMatchConfidence: number;
  musicbrainzContactEmail: string;
  audioDbApiKey: string;
  // Points at a MusicBrainz-API-compatible server other than the real
  // musicbrainz.org — a self-hosted mirror the operator runs themselves,
  // not a shortcut to someone else's infrastructure. Empty (the default)
  // uses the real musicbrainz.org. Applied at startup only — like
  // musicbrainzContactEmail/audioDbApiKey above, changing it needs a
  // restart.
  musicbrainzBaseUrl: string;
}

// TagWriteSettings mirrors config.TagWriteSettings' own negative polarity
// wire-for-wire (every field is "disableX", not "x") — see that Go type's
// doc comment for why. TagWriteToggleFields (in TagWriteSettingsCard) is
// what actually renders these as positive "which tags get written"
// checkboxes.
export interface TagWriteSettings {
  disableTitle: boolean;
  disableArtist: boolean;
  disableAlbumArtist: boolean;
  disableAlbum: boolean;
  disableTrackNumber: boolean;
  disableDiscNumber: boolean;
  disableDate: boolean;
  disableTrackTotal: boolean;
  disableDiscTotal: boolean;
  disableGenre: boolean;
  disableReleaseType: boolean;
  disableArtistSortName: boolean;
  disableAlbumArtistSortName: boolean;
  disableReleaseCountry: boolean;
  disableReleaseStatus: boolean;
  disableMedia: boolean;
  disableMood: boolean;
  disableComposer: boolean;
  disableCoverImage: boolean;
  disableMusicBrainzArtistId: boolean;
  disableAlbumArtistId: boolean;
  disableMusicBrainzAlbumId: boolean;
  disableMusicBrainzReleaseGroupId: boolean;
  disableMusicBrainzRecordingId: boolean;
}

// ViewPrefs is a signed-in account's own remembered Grid/Compact/List
// choice for the main artist library and an artist page's own Albums
// section — kept separate so one can be List while the other stays Grid.
// Empty string means "grid" (the default for both). Per-account, not
// per-browser (contrast theme.ts's theme preference) — see
// config.UserAccount.LibraryView/AlbumsView. Always {"",""} under plain
// API-key use (no login accounts configured): there's no per-account
// identity to remember it against.
export interface ViewPrefs {
  libraryView: string;
  albumsView: string;
}

const KEY_STORAGE = "cantinode-api-key";

export function getApiKey(): string {
  return localStorage.getItem(KEY_STORAGE) ?? "";
}

// proxiedImage routes a provider image (e.g. an artist photo from
// TheAudioDB) through CantiNode's caching proxy so it's served locally and
// survives the provider's URL rot. Local API URLs (our own /cover endpoint)
// pass through unchanged; empty URLs return undefined so callers can fall
// back.
export function proxiedImage(url?: string): string | undefined {
  if (!url) return undefined;
  if (url.startsWith("/")) return url;
  return `/api/v1/image?url=${encodeURIComponent(url)}&apikey=${encodeURIComponent(getApiKey())}`;
}

// musicAlbumCoverUrl points at the server's own Cover Art Archive proxy —
// an <img src>, so the API key rides the query string (no header needed).
export function musicAlbumCoverUrl(albumId: number): string {
  return `/api/v1/music/album/${albumId}/cover?apikey=${encodeURIComponent(getApiKey())}`;
}

// musicReleaseGroupCoverUrl is musicAlbumCoverUrl's counterpart for a
// wanted/missing album — no owned albums row (and so no specific release
// mbid) exists yet, so this resolves cover art via the release group's own
// cached representative release instead.
export function musicReleaseGroupCoverUrl(releaseGroupMbid: string): string {
  return `/api/v1/music/releasegroup/${encodeURIComponent(releaseGroupMbid)}/cover?apikey=${encodeURIComponent(getApiKey())}`;
}

export function setApiKey(key: string) {
  localStorage.setItem(KEY_STORAGE, key);
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    ...init,
    headers: { "X-Api-Key": getApiKey(), ...init?.headers },
  });
  if (!resp.ok) {
    let message = resp.statusText;
    try {
      const body = await resp.json();
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body; keep statusText
    }
    throw new ApiError(resp.status, message);
  }
  if (resp.status === 204) {
    return undefined as T;
  }
  return resp.json() as Promise<T>;
}

const json = (body: unknown): RequestInit => ({
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  systemStatus: () => request<SystemStatus>("/api/v1/system/status"),
  authStatus: () => request<AuthStatus>("/api/v1/auth/status"),
  // First-run wizard: only answers/claims on a fresh instance — no API key.
  setupStatus: () => request<{ needed: boolean }>("/api/v1/setup/status"),
  setupInstance: (username: string, password: string) =>
    request<{ ok: boolean }>("/api/v1/auth/setup", json({ username, password })),
  login: (username: string, password: string) =>
    request<{ ok: boolean }>("/api/v1/auth/login", json({ username, password })),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),
  getViewPrefs: () => request<ViewPrefs>("/api/v1/auth/view-prefs"),
  setViewPrefs: (prefs: Partial<ViewPrefs>) =>
    request<ViewPrefs>("/api/v1/auth/view-prefs", { ...json(prefs), method: "PUT" }),
  setCredentials: (username: string, password: string) =>
    request<{ authEnabled: boolean }>("/api/v1/auth/credentials", {
      ...json({ username, password }),
      method: "PUT",
    }),
  listUsers: () => request<{ users: UserAccount[] }>("/api/v1/auth/users"),
  addUser: (username: string, password: string, role: "admin" | "member" = "member") =>
    request<{ users: UserAccount[] }>("/api/v1/auth/users", json({ username, password, role })),
  removeUser: (username: string) =>
    request<{ users: UserAccount[] }>(`/api/v1/auth/users/${encodeURIComponent(username)}`, {
      method: "DELETE",
    }),
  setUserPassword: (username: string, password: string) =>
    request<{ ok: boolean }>(`/api/v1/auth/users/${encodeURIComponent(username)}/password`, {
      ...json({ password }),
      method: "PUT",
    }),
  makeDefaultUser: (username: string) =>
    request<{ users: UserAccount[] }>(`/api/v1/auth/users/${encodeURIComponent(username)}/default`, {
      method: "PUT",
    }),
  setUserRole: (username: string, role: "admin" | "member") =>
    request<{ users: UserAccount[] }>(`/api/v1/auth/users/${encodeURIComponent(username)}/role`, {
      ...json({ role }),
      method: "PUT",
    }),
  regenerateApiKey: () =>
    request<{ apiKey: string }>("/api/v1/auth/apikey/regenerate", {
      method: "POST",
    }),
  logTail: (lines = 200) =>
    request<{ lines: string[]; path: string }>(`/api/v1/log?lines=${lines}`),
  listBackups: () => request<BackupInfo[]>("/api/v1/backup"),
  createBackup: () => request<BackupInfo>("/api/v1/backup", { method: "POST" }),
  deleteBackup: (name: string) =>
    request<void>(`/api/v1/backup/${name}`, { method: "DELETE" }),
  restoreBackup: (name: string) =>
    request<{ staged: number; message: string }>(
      `/api/v1/backup/${name}/restore`,
      { method: "POST" },
    ),
  downloadBackup: async (name: string): Promise<Blob> => {
    const resp = await fetch(`/api/v1/backup/${name}/download`, {
      headers: { "X-Api-Key": getApiKey() },
    });
    if (!resp.ok) throw new ApiError(resp.status, resp.statusText);
    return resp.blob();
  },
  health: () => request<HealthResult>("/api/v1/health"),
  checkHealth: () =>
    request<HealthResult>("/api/v1/health/check", { method: "POST" }),

  listDownloadClients: () =>
    request<DownloadClient[]>("/api/v1/downloadclient"),
  addDownloadClient: (c: Omit<DownloadClient, "id">) =>
    request<DownloadClient>("/api/v1/downloadclient", json(c)),
  updateDownloadClient: (c: DownloadClient) =>
    request<DownloadClient>(`/api/v1/downloadclient/${c.id}`, {
      ...json(c),
      method: "PUT",
    }),
  deleteDownloadClient: (id: number) =>
    request<void>(`/api/v1/downloadclient/${id}`, { method: "DELETE" }),
  testDownloadClient: (c: Omit<DownloadClient, "id">) =>
    request<{ ok: boolean }>("/api/v1/downloadclient/test", json(c)),
  blocklist: () => request<BlockEntry[]>("/api/v1/blocklist"),
  unblock: (id: number) =>
    request<void>(`/api/v1/blocklist/${id}`, { method: "DELETE" }),
  queue: () =>
    request<{ items: QueueItem[]; errors: string[] }>("/api/v1/queue"),
  triggerImport: () =>
    request<{ status: string }>("/api/v1/queue/import", { method: "POST" }),
  importStatus: () => request<ImportState>("/api/v1/queue/import/status"),
  removeQueueItem: (clientConfigId: number, itemId: string, grabId?: number) =>
    request<{ removed: string }>(
      `/api/v1/queue/${clientConfigId}/${encodeURIComponent(itemId)}${grabId ? `?grabId=${grabId}` : ""}`,
      { method: "DELETE" },
    ),
  history: (search = "", limit = 100, offset = 0) =>
    request<{ records: GrabRecord[]; total: number }>(
      `/api/v1/history?search=${encodeURIComponent(search)}&limit=${limit}&offset=${offset}`,
    ),
  cancelGrab: (id: number) =>
    request<{ cancelled: number }>(`/api/v1/grab/${id}/cancel`, { method: "POST" }),
  clearHistory: () => request<{ cleared: number }>("/api/v1/history", { method: "DELETE" }),

  listProfiles: () => request<QualityProfile[]>("/api/v1/qualityprofile"),
  addProfile: (p: Partial<QualityProfile>) =>
    request<QualityProfile>("/api/v1/qualityprofile", json(p)),
  updateProfile: (p: QualityProfile) =>
    request<QualityProfile>(`/api/v1/qualityprofile/${p.id}`, {
      ...json(p),
      method: "PUT",
    }),
  deleteProfile: (id: number) =>
    request<void>(`/api/v1/qualityprofile/${id}`, { method: "DELETE" }),
  setDefaultProfile: (id: number) =>
    request<QualityProfile>(`/api/v1/qualityprofile/${id}/default`, {
      method: "PUT",
    }),

  listIndexers: () => request<Indexer[]>("/api/v1/indexer"),
  listNativeIndexers: () => request<NativeIndexer[]>("/api/v1/indexer/native"),
  addIndexer: (ind: Omit<Indexer, "id" | "addedAt">) =>
    request<Indexer>("/api/v1/indexer", json(ind)),
  updateIndexer: (ind: Indexer) =>
    request<Indexer>(`/api/v1/indexer/${ind.id}`, {
      ...json(ind),
      method: "PUT",
    }),
  deleteIndexer: (id: number) =>
    request<void>(`/api/v1/indexer/${id}`, { method: "DELETE" }),
  testIndexer: (ind: Omit<Indexer, "id" | "addedAt">) =>
    request<{ ok: boolean }>("/api/v1/indexer/test", json(ind)),

  getNamingSettings: () => request<NamingSettings>("/api/v1/settings/naming"),
  saveNamingSettings: (templates: Partial<NamingSettings>) =>
    request<NamingSettings>("/api/v1/settings/naming", {
      ...json(templates),
      method: "PUT",
    }),

  getPathMappings: () => request<PathMapping[]>("/api/v1/settings/pathmappings"),
  savePathMappings: (mappings: PathMapping[]) =>
    request<PathMapping[]>("/api/v1/settings/pathmappings", {
      ...json(mappings),
      method: "PUT",
    }),

  getTimingSettings: () => request<TimingSettings>("/api/v1/settings/timings"),
  saveTimingSettings: (settings: TimingSettings) =>
    request<TimingSettings>("/api/v1/settings/timings", {
      ...json(settings),
      method: "PUT",
    }),

  listRootFolders: () => request<RootFolder[]>("/api/v1/rootfolder"),
  browseFolders: (path?: string) =>
    request<FolderListing>(
      `/api/v1/filesystem${path ? `?path=${encodeURIComponent(path)}` : ""}`,
    ),
  addRootFolder: (mediaType: string, path: string, name?: string) =>
    request<RootFolder>("/api/v1/rootfolder", json({ mediaType, path, name })),
  deleteRootFolder: (id: number) =>
    request<void>(`/api/v1/rootfolder/${id}`, { method: "DELETE" }),
  renameRootFolder: (id: number, name: string) =>
    request<void>(`/api/v1/rootfolder/${id}/name`, { ...json({ name }), method: "PUT" }),
  setDefaultRootFolder: (id: number) =>
    request<void>(`/api/v1/rootfolder/${id}/default`, { method: "PUT" }),

  clearAllCache: () =>
    request<{ removed: number; freedBytes: number }>(
      "/api/v1/cache",
      { method: "DELETE" },
    ),

  // --- Music ---
  listMusicArtists: () => request<MusicArtist[]>("/api/v1/music/artist"),
  searchMusicArtists: (query: string) =>
    request<MusicBrainzArtistResult[]>(
      `/api/v1/music/artist/search?query=${encodeURIComponent(query)}`,
    ),
  monitorMusicArtist: (mbid: string) =>
    request<MusicArtist>("/api/v1/music/artist", json({ mbid })),
  // Lighter sibling of monitorMusicArtist for the auto-match panel's
  // "artist not in your library" search: caches just the discography (all
  // the album-matching step needs right away), skipping the version/
  // tracklist pre-fetch and bio/photo lookup — the next scan's own
  // backfill picks those up automatically. See internal/api's
  // handleQuickAddMusicArtist.
  quickAddMusicArtist: (mbid: string) =>
    request<MusicArtist>("/api/v1/music/artist/quick", json({ mbid })),
  // Adds a MusicBrainz Series (e.g. a numbered compilation series like
  // "Now That's What I Call Music!") as a synthetic library artist — a
  // second way into the library beyond one real artist at a time. input is
  // sent as raw pasted text (a full series URL or a bare MBID); the
  // backend is the only place that parses/validates it, since it's also
  // the only place that can authoritatively resolve it against MusicBrainz
  // anyway. Behaves like monitoring a real artist from here on.
  addMusicSeries: (input: string) =>
    request<MusicArtist>("/api/v1/music/series", json({ input })),
  getMusicArtist: (id: number) => request<MusicArtist>(`/api/v1/music/artist/${id}`),
  unmonitorMusicArtist: (id: number) =>
    request<void>(`/api/v1/music/artist/${id}/unmonitor`, { method: "POST" }),
  refreshMusicArtist: (id: number) =>
    request<void>(`/api/v1/music/artist/${id}/refresh`, { method: "POST" }),
  listMissingMusicReleaseGroups: (id: number) =>
    request<MusicReleaseGroup[]>(`/api/v1/music/artist/${id}/missing`),
  listPlaylists: () => request<Playlist[]>("/api/v1/music/playlist"),
  createPlaylist: (name: string, description: string) =>
    request<Playlist>("/api/v1/music/playlist", json({ name, description })),
  getPlaylist: (id: number) => request<PlaylistDetail>(`/api/v1/music/playlist/${id}`),
  updatePlaylist: (id: number, name: string, description: string) =>
    request<Playlist>(`/api/v1/music/playlist/${id}`, {
      ...json({ name, description }),
      method: "PUT",
    }),
  deletePlaylist: (id: number) =>
    request<void>(`/api/v1/music/playlist/${id}`, { method: "DELETE" }),
  addPlaylistItem: (playlistId: number, trackId: number) =>
    request<PlaylistTrack>(`/api/v1/music/playlist/${playlistId}/items`, json({ trackId })),
  addPlaylistItemsBulk: (playlistId: number, trackIds: number[]) =>
    request<PlaylistTrack[]>(`/api/v1/music/playlist/${playlistId}/items/bulk`, json({ trackIds })),
  removePlaylistItem: (playlistId: number, itemId: number) =>
    request<void>(`/api/v1/music/playlist/${playlistId}/items/${itemId}`, { method: "DELETE" }),
  reorderPlaylistItems: (playlistId: number, itemIds: number[]) =>
    request<PlaylistTrack[]>(`/api/v1/music/playlist/${playlistId}/items/order`, {
      ...json({ itemIds }),
      method: "PUT",
    }),
  exportPlaylist: async (id: number): Promise<Blob> => {
    const resp = await fetch(`/api/v1/music/playlist/${id}/export`, {
      headers: { "X-Api-Key": getApiKey() },
    });
    if (!resp.ok) throw new ApiError(resp.status, resp.statusText);
    return resp.blob();
  },
  searchOwnedTracks: (q: string) =>
    request<TrackSearchResult[]>(`/api/v1/music/track/search?q=${encodeURIComponent(q)}`),
  listPlaylistsForTrack: (trackId: number) =>
    request<Playlist[]>(`/api/v1/music/track/${trackId}/playlists`),
  musicCalendar: (from?: string, to?: string) => {
    const params = new URLSearchParams();
    if (from) params.set("from", from);
    if (to) params.set("to", to);
    const qs = params.toString();
    return request<CalendarEntry[]>(`/api/v1/music/calendar${qs ? `?${qs}` : ""}`);
  },
  listReleaseGroupVersions: (releaseGroupMbid: string) =>
    request<ReleaseGroupVersion[]>(
      `/api/v1/music/releasegroup/${encodeURIComponent(releaseGroupMbid)}/versions`,
    ),
  getReleaseGroupTracks: (releaseGroupMbid: string) =>
    request<ReleaseGroupTracklist>(
      `/api/v1/music/releasegroup/${encodeURIComponent(releaseGroupMbid)}/tracks`,
    ),
  removeMusicArtist: (id: number, deleteFiles = false) =>
    request<{ deleted: boolean }>(
      `/api/v1/music/artist/${id}${deleteFiles ? "?deleteFiles=true" : ""}`,
      { method: "DELETE" },
    ),
  listMusicAlbums: (artistId: number) =>
    request<MusicAlbum[]>(`/api/v1/music/artist/${artistId}/albums`),
  getMusicAlbum: (id: number) => request<MusicAlbum>(`/api/v1/music/album/${id}`),
  listMusicTracks: (albumId: number) =>
    request<MusicTrack[]>(`/api/v1/music/album/${albumId}/tracks`),
  removeMusicAlbum: (id: number, deleteFiles = false) =>
    request<{ deleted: boolean }>(
      `/api/v1/music/album/${id}${deleteFiles ? "?deleteFiles=true" : ""}`,
      { method: "DELETE" },
    ),
  previewOrganizeMusicAlbum: (id: number) =>
    request<{ moves: RenameMove[] }>(`/api/v1/music/album/${id}/organize/preview`),
  organizeMusicAlbum: (id: number) =>
    request<{ moves: RenameMove[]; errors: string[] }>(
      `/api/v1/music/album/${id}/organize`,
      { method: "POST" },
    ),
  writeMusicTagsForAlbum: (id: number, clear = false) =>
    request<{ written: number; errors: string[] }>(
      `/api/v1/music/album/${id}/write-tags`,
      json({ clear }),
    ),
  scanMusicAlbum: (id: number) =>
    request<MusicScanResult>(`/api/v1/music/album/${id}/scan`, { method: "POST" }),
  // Only worth calling when the just-loaded MusicAlbum's own
  // descriptionFetchedAt is unset — see that field's own doc comment.
  // Once fetched (even to nothing), the plain getMusicAlbum response
  // already carries the cached description for free.
  // mood (TheAudioDB's own field, e.g. "Trippy") is cached alongside the
  // description in the same response, for internal/tagwriter's own use —
  // not currently surfaced in this view.
  getMusicAlbumDescription: (id: number) =>
    request<{ description: string; mood?: string }>(`/api/v1/music/album/${id}/description`),
  // The album/artist page's own "retry cover art" button, shown over the
  // fallback tile once the plain <img src={musicAlbumCoverUrl(...)}>
  // fails to load — forces a fresh check of both cover art sources rather
  // than waiting on their own ~30-day self-heal. found:false means the
  // retry itself succeeded, it just confirmed neither source has this
  // release right now.
  retryMusicAlbumCover: (id: number) =>
    request<{ found: boolean }>(`/api/v1/music/album/${id}/cover/retry`, { method: "POST" }),
  searchAlbumUpgrade: (id: number) =>
    request<{ releases: ReleaseCandidate[]; errors: string[] }>(`/api/v1/music/album/${id}/upgrade/search`),
  grabAlbumUpgrade: (
    id: number,
    title: string,
    downloadUrl: string,
    protocol: string,
    guid: string = "",
  ) =>
    request<{ client: string; id?: string }>(
      `/api/v1/music/album/${id}/upgrade/grab`,
      json({ title, downloadUrl, protocol, guid }),
    ),
  listMusicTrackFiles: (trackId: number) =>
    request<MusicTrackFile[]>(`/api/v1/music/track/${trackId}/files`),
  getMusicTrackFileTags: (id: number) =>
    request<MusicTrackFileTags>(`/api/v1/music/trackfile/${id}/tags`),
  listUnmatchedTrackFiles: () =>
    request<UnmatchedTrackFile[]>("/api/v1/music/trackfile/unmatched"),
  suggestTrackFileMatches: (fileIds: number[], releaseGroupMbid: string, releaseMbid = "") =>
    request<{ releaseTitle: string; suggestions: TrackSuggestion[] }>(
      "/api/v1/music/trackfile/match-suggest",
      json({ fileIds, releaseGroupMbid, releaseMbid }),
    ),
  searchMusicBrainzRecordings: (artist: string, album: string, title: string) =>
    request<MusicBrainzRecordingResult[]>(
      `/api/v1/music/musicbrainz/search?artist=${encodeURIComponent(artist)}&album=${encodeURIComponent(album)}&title=${encodeURIComponent(title)}`,
    ),
  matchTrackFile: (id: number, recordingMbid: string, releaseMbid = "") =>
    request<MusicTrackFile>(
      `/api/v1/music/trackfile/${id}/match`,
      json({ recordingMbid, releaseMbid }),
    ),
  clearTrackFileMatch: (id: number) =>
    request<void>(`/api/v1/music/trackfile/${id}/match`, { method: "DELETE" }),
  deleteTrackFile: (id: number) =>
    request<void>(`/api/v1/music/trackfile/${id}`, { method: "DELETE" }),
  previewOrganizeMusicArtist: (id: number) =>
    request<{ moves: RenameMove[] }>(`/api/v1/music/artist/${id}/organize/preview`),
  organizeMusicArtist: (id: number) =>
    request<{ moves: RenameMove[]; errors: string[] }>(
      `/api/v1/music/artist/${id}/organize`,
      { method: "POST" },
    ),
  writeMusicTagsForArtist: (id: number, clear = false) =>
    request<{ written: number; errors: string[] }>(
      `/api/v1/music/artist/${id}/write-tags`,
      json({ clear }),
    ),
  previewMoveMusicArtist: (id: number, rootFolderId: number) =>
    request<{ moves: ArtistMove[]; totalBytes: number }>(
      `/api/v1/music/artist/${id}/move/preview?rootFolderId=${rootFolderId}`,
    ),
  moveMusicArtist: (id: number, rootFolderId: number) =>
    request<{ status: string }>(`/api/v1/music/artist/${id}/move`, json({ rootFolderId })),
  musicMoveStatus: () => request<MusicMoveState>("/api/v1/music/move/status"),
  triggerMusicScan: () =>
    request<{ status: string }>("/api/v1/music/scan", { method: "POST" }),
  musicScanStatus: () => request<MusicScanState>("/api/v1/music/scan/status"),
  wantMusicAlbum: (artistId: number, releaseGroupMbid: string, monitor: boolean) =>
    request<WantedAlbum>(
      `/api/v1/music/artist/${artistId}/wanted`,
      json({ releaseGroupMbid, monitor }),
    ),
  listWantedMusicAlbums: (artistId: number) =>
    request<WantedAlbum[]>(`/api/v1/music/artist/${artistId}/wanted`),
  removeWantedMusicAlbum: (id: number) =>
    request<void>(`/api/v1/music/wanted/${id}`, { method: "DELETE" }),
  searchWantedMusicAlbum: (id: number) =>
    request<{ releases: ReleaseCandidate[]; errors: string[] }>(`/api/v1/music/wanted/${id}/search`),
  grabWantedMusicAlbum: (
    id: number,
    title: string,
    downloadUrl: string,
    protocol: string,
    guid: string = "",
  ) =>
    request<{ client: string; id?: string }>(
      `/api/v1/music/wanted/${id}/grab`,
      json({ title, downloadUrl, protocol, guid }),
    ),
  getMusicSettings: () => request<MusicSettings>("/api/v1/settings/music"),
  saveMusicSettings: (s: MusicSettings) =>
    request<MusicSettings>("/api/v1/settings/music", { ...json(s), method: "PUT" }),
  getTagWriteSettings: () => request<TagWriteSettings>("/api/v1/settings/tagwrite"),
  saveTagWriteSettings: (s: TagWriteSettings) =>
    request<TagWriteSettings>("/api/v1/settings/tagwrite", { ...json(s), method: "PUT" }),
};

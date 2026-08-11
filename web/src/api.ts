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
  mediaType: string;
  path: string;
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
}

export interface RenameMove {
  fileId: number;
  from: string;
  to: string;
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
}

export interface MusicAlbum {
  id: number;
  artistId: number;
  mbid: string;
  releaseGroupMbid: string;
  title: string;
  releaseDate: string;
  primaryType: string;
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

export interface MusicReleaseGroup {
  id: number;
  artistId: number;
  releaseGroupMbid: string;
  title: string;
  primaryType: string;
  secondaryTypes: string[];
  firstReleaseDate: string;
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

export interface MusicSettings {
  organizeOnMatch: boolean;
  minMatchConfidence: number;
  musicbrainzContactEmail: string;
  audioDbApiKey: string;
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
  addRootFolder: (mediaType: string, path: string) =>
    request<RootFolder>("/api/v1/rootfolder", json({ mediaType, path })),
  deleteRootFolder: (id: number) =>
    request<void>(`/api/v1/rootfolder/${id}`, { method: "DELETE" }),

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
  getMusicArtist: (id: number) => request<MusicArtist>(`/api/v1/music/artist/${id}`),
  unmonitorMusicArtist: (id: number) =>
    request<void>(`/api/v1/music/artist/${id}/unmonitor`, { method: "POST" }),
  refreshMusicArtist: (id: number) =>
    request<void>(`/api/v1/music/artist/${id}/refresh`, { method: "POST" }),
  listMissingMusicReleaseGroups: (id: number) =>
    request<MusicReleaseGroup[]>(`/api/v1/music/artist/${id}/missing`),
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
  scanMusicAlbum: (id: number) =>
    request<MusicScanResult>(`/api/v1/music/album/${id}/scan`, { method: "POST" }),
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
  listUnmatchedTrackFiles: () =>
    request<MusicTrackFile[]>("/api/v1/music/trackfile/unmatched"),
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
  previewOrganizeTrackFile: (id: number) =>
    request<{ path: string }>(`/api/v1/music/trackfile/${id}/organize/preview`),
  organizeTrackFile: (id: number) =>
    request<{ path: string }>(`/api/v1/music/trackfile/${id}/organize`, { method: "POST" }),
  writeMusicTags: (id: number) =>
    request<void>(`/api/v1/music/trackfile/${id}/write-tags`, { method: "POST" }),
  deleteTrackFile: (id: number) =>
    request<void>(`/api/v1/music/trackfile/${id}`, { method: "DELETE" }),
  previewOrganizeMusicArtist: (id: number) =>
    request<{ moves: RenameMove[] }>(`/api/v1/music/artist/${id}/organize/preview`),
  organizeMusicArtist: (id: number) =>
    request<{ moves: RenameMove[]; errors: string[] }>(
      `/api/v1/music/artist/${id}/organize`,
      { method: "POST" },
    ),
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
};

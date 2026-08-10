// Typed client for LibriNode's REST API. The API key is kept in localStorage
// for now; proper session handling comes with the settings UI.

export interface SystemStatus {
  appName: string;
  version: string;
  appVersion?: string;
  os: string;
  arch: string;
  uptime: string;
  dataDir: string;
  startTime: string;
  ipAddresses: string[];
  port: number;
}

export interface Author {
  id: number;
  metadataSource: string;
  foreignAuthorId: string;
  name: string;
  sortName: string;
  description: string;
  imageUrl: string;
  monitored: boolean;
  inEbookLibrary: boolean;
  providerOverride: string;
  bookCount?: number;
  ownedCount: number;
  books?: Book[];
}

export interface Book {
  id: number;
  authorId: number;
  foreignBookId: string;
  // "book" for prose; "comic" volumes carry their series' type.
  mediaType: string;
  title: string;
  sortTitle: string;
  description: string;
  releaseDate: string;
  rating: number;
  coverUrl: string;
  monitored: boolean;
  inEbookLibrary: boolean;
  ebookMonitored: boolean;
  hasFile: boolean;
  hasEbookFile: boolean;
  editions?: Edition[];
  series?: SeriesLink[];
  files?: BookFile[];
}

export interface Edition {
  id: number;
  bookId: number;
  foreignEditionId: string;
  title: string;
  isbn13: string;
  asin: string;
  format: string;
  publisher: string;
  language: string;
  releaseDate: string;
  coverUrl: string;
  monitored: boolean;
}

export interface SeriesLink {
  seriesId: number;
  title: string;
  position: number;
}

export interface BookFile {
  id: number;
  rootFolderId: number;
  bookId?: number;
  mediaType: string;
  path: string;
  size: number;
  format: string;
  modifiedAt: string;
  addedAt: string;
}

export interface RootFolder {
  id: number;
  mediaType: string;
  path: string;
  accessible: boolean;
}

export interface ScanResult {
  roots: number;
  scanned: number;
  matched: number;
  unmatched: number;
  removed: number;
  errors?: string[];
}

export interface Indexer {
  id: number;
  name: string;
  // "newznab" | "torznab" | a native implementation name.
  type: string;
  baseUrl: string;
  apiKey: string;
  categories: string;
  audioCategories: string;
  comicCategories: string;
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
  // Set when the item belongs to a tracked grab — links it to its book.
  grabId?: number;
  bookId?: number;
  mediaType?: string;
}

export interface SeriesResult {
  foreignSeriesId: string;
  title: string;
  description?: string;
  authorName?: string;
  year?: number;
  coverUrl?: string;
  issueCount: number;
}

export interface Series {
  id: number;
  metadataSource: string;
  foreignSeriesId: string;
  title: string;
  description: string;
  mediaType: string;
  monitored: boolean;
  monitorNew: boolean;
  providerOverride: string;
  coverUrl: string;
  itemCount: number;
  ownedCount: number;
  volumes?: Book[];
}

export interface Release {
  indexerId: number;
  indexer: string;
  protocol: "usenet" | "torrent";
  title: string;
  guid: string;
  infoUrl?: string;
  downloadUrl: string;
  size: number;
  publishDate?: string;
  seeders: number;
  peers: number;
}

export interface ReleaseCandidate extends Release {
  parsed: {
    author?: string;
    title?: string;
    year?: number;
    formats?: string[];
    language?: string;
    retail: boolean;
    // Comic volume number the release names, and the range end when it
    // spans several ("v01-v12"); 0/absent for a single-volume release.
    volume?: number;
    volumeEnd?: number;
    // The release declares itself a complete run ("Complete", "Collection")
    // even without a volume range — e.g. an ebook series bundle.
    pack?: boolean;
  };
  score: number;
  approved: boolean;
  rejections?: string[];
}

export interface SearchOutcome {
  bookId: number;
  bookTitle: string;
  grabbed: boolean;
  release?: string;
  client?: string;
  message?: string;
}

export interface GrabRecord {
  id: number;
  bookId?: number;
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

export interface LibraryStatus {
  mediaType: string;
  active: boolean;
  items: number;
  wanted: number;
}

export interface HomeItem {
  bookId: number;
  authorId?: number;
  seriesId?: number;
  title: string;
  subtitle?: string;
  coverUrl?: string;
  hasFile: boolean;
  releaseDate?: string;
  rating?: number;
  seriesTitle?: string;
  seriesPosition?: number;
}

export interface CalendarItem {
  bookId: number;
  authorId?: number;
  seriesId?: number;
  title: string;
  subtitle?: string;
  mediaType: string;
  releaseDate: string;
  owned: boolean;
}

export interface HomeSection {
  mediaType: string;
  items: number;
  wantedCount: number;
  recentlyAdded: HomeItem[];
  wanted: HomeItem[];
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
  ebookFolder: string;
  ebookFile: string;
  comicFolder: string;
  comicFile: string;
  musicFile: string;
  tokens: string[];
  example: string;
  musicExample: string;
}

export interface RenameMove {
  fileId: number;
  bookId: number;
  bookTitle: string;
  from: string;
  to: string;
}

export interface RenameResult {
  moves: RenameMove[];
  skips: string[];
  // Library-scope organize also cleans: files that don't belong in the
  // library (previewed; deleted on apply with deleteUnwanted) and, on apply,
  // how many were deleted and how many empty folders were pruned.
  cleanups?: { path: string; size: number }[];
  deleted?: number;
  prunedDirs?: number;
}

// Metadata provider search results (not yet in the library).
export interface SearchAuthor {
  foreignAuthorId: string;
  name: string;
  imageUrl: string;
  bookCount?: number;
}

export interface SearchBook {
  foreignBookId: string;
  title: string;
  authorName: string;
  releaseDate: string;
  rating: number;
  coverUrl: string;
}

export interface ProviderSettings {
  token: string;
}

export interface MetadataSettings {
  active: string;
  available: string[];
  // Ordered book providers consulted only when `active` finds nothing —
  // a subset of `available`, never including `active`.
  fallbacks: string[];
  providers: Record<string, ProviderSettings>;
  comicProviders: string[];
  comicProvider: string;
  comicCoverSource: string;
  language: string;
  country: string;
  includeAdult: boolean;
  includeCompilations: boolean;
}

export interface ImportSettings {
  packImportAll: boolean;
  removeCompleted: boolean;
  deleteCompletedFiles: boolean;
}

// PathMapping translates a download client's reported path prefix into the
// path where LibriNode sees the same files (remote client / container
// setups). Longest matching prefix wins.
export interface PathMapping {
  remotePrefix: string;
  localPrefix: string;
}

// TimingSettings: background loop cadences; 0 = use the built-in default.
// Changes apply on the next server start.
export interface TimingSettings {
  searchIntervalHours: number;
  refreshIntervalHours: number;
  healthIntervalMinutes: number;
  importIntervalSeconds: number;
}

// UserAccount is one login; the default user is protected from removal.
export interface UserAccount {
  username: string;
  default: boolean;
  role: "admin" | "member";
}

// UnmatchedOption is an unmatched file plus its existing-file import choices:
// the parsed author, a confident suggestion when the filename singles out one
// book, and the author's importable books (owned-in-format excluded).
export interface UnmatchedOption {
  file: BookFile;
  authorName?: string;
  authorId?: number;
  // Series-first (comic) library: the parsed series and volume number.
  seriesName?: string;
  seriesId?: number;
  volume?: number;
  suggested?: number;
  confident: boolean;
  confidence: number; // 0–100
  candidates: { id: number; title: string; year?: string }[];
  // Set when this file duplicates a book already owned in the library.
  duplicate?: {
    bookId: number;
    title: string;
    year?: string;
    file: BookFile; // the library's current copy
    confidence: number;
  };
}

// FolderListing is one level of the server's filesystem for the folder picker.
export interface FolderListing {
  path: string;
  parent: string;
  directories: { name: string; path: string }[];
}

// --- Music: a separate domain (artists/albums/tracks, not authors/books) —
// see internal/musiclibrary's package doc comment on the Go side. ---

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
  status: "wanted" | "downloading" | "downloaded" | "ignored";
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

const KEY_STORAGE = "librinode-api-key";

export function getApiKey(): string {
  return localStorage.getItem(KEY_STORAGE) ?? "";
}

// proxiedImage routes a provider image (Hardcover/AniList/ComicVine art)
// through LibriNode's caching proxy so it's served locally and survives the
// provider's URL rot. Local API URLs (our own /cover endpoint) pass through
// unchanged; empty URLs return undefined so callers can fall back.
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
  libraries: () => request<LibraryStatus[]>("/api/v1/libraries"),
  home: () => request<HomeSection[]>("/api/v1/home"),
  wanted: (library: string) =>
    request<{ items: HomeItem[] }>(`/api/v1/wanted?library=${library}`),
  calendar: (past = 30, days = 90) =>
    request<{ items: CalendarItem[]; from: string; to: string }>(
      `/api/v1/calendar?past=${past}&days=${days}`,
    ),
  setBookLibrary: (
    id: number,
    library: string,
    member: boolean,
    monitored: boolean,
    deleteFiles = false,
  ) =>
    request<Book>(`/api/v1/book/${id}/library`, {
      ...json({ library, member, monitored, deleteFiles }),
      method: "PUT",
    }),

  searchAuthors: (term: string) =>
    request<SearchAuthor[]>(
      `/api/v1/search?type=author&term=${encodeURIComponent(term)}`,
    ),
  searchBooks: (term: string) =>
    request<SearchBook[]>(
      `/api/v1/search?type=book&term=${encodeURIComponent(term)}`,
    ),

  listAuthors: (library?: string) =>
    request<Author[]>(`/api/v1/author${library ? `?library=${library}` : ""}`),
  getAuthor: (id: number) => request<Author>(`/api/v1/author/${id}`),
  addAuthor: (foreignAuthorId: string, library: string = "ebook") =>
    request<Author>("/api/v1/author", json({ foreignAuthorId, library })),
  refreshAuthor: (id: number) =>
    request<Author>(`/api/v1/author/${id}/refresh`, { method: "POST" }),
  setAuthorProvider: (id: number, provider: string) =>
    request<Author>(`/api/v1/author/${id}/provider`, {
      ...json({ provider }),
      method: "PUT",
    }),
  authorMissing: (id: number, library: string) =>
    request<Book[]>(`/api/v1/author/${id}/missing?library=${library}`),
  searchAuthorWanted: (id: number, library: string) =>
    request<{ searched: number; grabbed: number; outcomes: SearchOutcome[] }>(
      `/api/v1/author/${id}/search?library=${library}`,
      { method: "POST" },
    ),
  removeAuthorFromLibrary: (id: number, library: string, deleteFiles: boolean) =>
    request<unknown>(`/api/v1/author/${id}/library`, {
      ...json({ library, member: false, deleteFiles }),
      method: "PUT",
    }),
  // Scope with authorId (one author's books) or library (a format library's
  // member books, filtered server-side); omit both only where the whole
  // library's books are genuinely needed (e.g. global search).
  listBooks: (authorId?: number, library?: "ebook") =>
    request<Book[]>(
      authorId
        ? `/api/v1/book?authorId=${authorId}`
        : library
          ? `/api/v1/book?library=${library}`
          : "/api/v1/book",
    ),
  getBook: (id: number) => request<Book>(`/api/v1/book/${id}`),
  addBook: (foreignBookId: string, library: string = "ebook") =>
    request<Book>("/api/v1/book", json({ foreignBookId, library })),
  monitorBook: (id: number, monitored: boolean) =>
    request(`/api/v1/book/${id}/monitor`, {
      ...json({ monitored }),
      method: "PUT",
    }),
  // Omit mediaType to scan every library; pass one to scan only that
  // library's root folders (the Scan-files button on a library/author/series
  // page passes its own type).
  scan: (mediaType?: string) =>
    request<ScanResult>(
      `/api/v1/library/scan${mediaType ? `?mediaType=${encodeURIComponent(mediaType)}` : ""}`,
      { method: "POST" },
    ),
  renamePreview: (authorId?: number, seriesId?: number, mediaType?: string, bookId?: number) =>
    request<RenameResult>(
      `/api/v1/library/rename${
        bookId
          ? `?bookId=${bookId}`
          : seriesId
            ? `?seriesId=${seriesId}`
            : authorId
              ? `?authorId=${authorId}`
              : mediaType
                ? `?mediaType=${mediaType}`
                : ""
      }`,
    ),
  renameApply: (
    authorId?: number,
    seriesId?: number,
    mediaType?: string,
    bookId?: number,
    deleteUnwanted?: boolean,
  ) =>
    request<RenameResult>("/api/v1/library/rename", {
      ...json(
        bookId
          ? { bookId }
          : seriesId
            ? { seriesId }
            : authorId
              ? { authorId }
              : mediaType
                ? { mediaType, deleteUnwanted: !!deleteUnwanted }
                : {},
      ),
      method: "POST",
    }),
  unmatchedOptions: (mediaType: string) =>
    request<UnmatchedOption[]>(
      `/api/v1/bookfile/unmatched/options?mediaType=${mediaType}`,
    ),
  importMatched: (mediaType: string) =>
    request<{ imported: number; needsReview: number; messages: string[] }>(
      "/api/v1/bookfile/import-matched",
      json({ mediaType }),
    ),
  matchFile: (fileId: number, bookId: number) =>
    request<{ file: BookFile; skips: string[] }>(
      `/api/v1/bookfile/${fileId}/match`,
      json({ bookId }),
    ),
  replaceFile: (fileId: number, bookId: number) =>
    request<{ file: BookFile; skips: string[]; deletedFiles: number; errors: string[] }>(
      `/api/v1/bookfile/${fileId}/replace`,
      json({ bookId }),
    ),
  dismissFile: (fileId: number, deleteFiles = false) =>
    request<void>(`/api/v1/bookfile/${fileId}${deleteFiles ? "?deleteFiles=true" : ""}`, {
      method: "DELETE",
    }),

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
  grabRelease: (
    title: string,
    downloadUrl: string,
    protocol: string,
    bookId?: number,
    mediaType: string = "ebook",
    guid: string = "",
  ) =>
    request<{ client: string; id?: string; grabId: number }>(
      "/api/v1/release/grab",
      json({ title, downloadUrl, protocol, bookId, mediaType, guid }),
    ),
  blocklist: () => request<BlockEntry[]>("/api/v1/blocklist"),
  unblock: (id: number) =>
    request<void>(`/api/v1/blocklist/${id}`, { method: "DELETE" }),
  searchReleasesForBook: (bookId: number, mediaType: string = "ebook") =>
    request<{ releases: ReleaseCandidate[]; errors: string[] }>(
      `/api/v1/release?bookId=${bookId}&mediaType=${mediaType}`,
    ),
  searchSeriesPacks: (seriesId: number) =>
    request<{
      releases: ReleaseCandidate[];
      errors: string[];
      grabBookId: number;
      missing: number;
    }>(`/api/v1/release/packs?seriesId=${seriesId}`),
  autoSearchBook: (bookId: number, mediaType: string = "ebook") =>
    request<SearchOutcome>(
      `/api/v1/book/${bookId}/search?mediaType=${mediaType}`,
      { method: "POST" },
    ),
  searchWanted: () =>
    request<{ searched: number; grabbed: number; outcomes: SearchOutcome[] }>(
      "/api/v1/library/search",
      { method: "POST" },
    ),
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
  runImport: () =>
    request<ImportResult>("/api/v1/library/import", { method: "POST" }),

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

  getImportSettings: () => request<ImportSettings>("/api/v1/settings/import"),
  saveImportSettings: (settings: ImportSettings) =>
    request<ImportSettings>("/api/v1/settings/import", {
      ...json(settings),
      method: "PUT",
    }),

  refreshLibrary: (mediaType: string) =>
    request<{ started: number; message: string }>(
      "/api/v1/library/refresh",
      json({ mediaType }),
    ),

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

  searchSeries: (term: string, mediaType: string) =>
    request<SeriesResult[]>(
      `/api/v1/search?term=${encodeURIComponent(term)}&type=${mediaType}`,
    ),
  listSeries: () => request<Series[]>("/api/v1/series"),
  addSeries: (mediaType: string, foreignSeriesId: string) =>
    request<Series>("/api/v1/series", json({ mediaType, foreignSeriesId })),
  getSeries: (id: number) => request<Series>(`/api/v1/series/${id}`),
  setSeriesProvider: (id: number, provider: string) =>
    request<Series>(`/api/v1/series/${id}/provider`, {
      ...json({ provider }),
      method: "PUT",
    }),
  monitorSeries: (id: number, monitored: boolean, monitorNew: boolean) =>
    request<Series>(`/api/v1/series/${id}/monitor`, {
      ...json({ monitored, monitorNew }),
      method: "PUT",
    }),
  refreshSeries: (id: number) =>
    request<Series>(`/api/v1/series/${id}/refresh`, { method: "POST" }),
  searchSeriesWanted: (id: number) =>
    request<{ searched: number; grabbed: number; outcomes: SearchOutcome[] }>(
      `/api/v1/series/${id}/search`,
      { method: "POST" },
    ),
  deleteSeries: (id: number, deleteFiles = false) =>
    request<{ deletedFiles: number; errors: string[] } | undefined>(
      `/api/v1/series/${id}${deleteFiles ? "?deleteFiles=true" : ""}`,
      { method: "DELETE" },
    ),

  listRootFolders: () => request<RootFolder[]>("/api/v1/rootfolder"),
  browseFolders: (path?: string) =>
    request<FolderListing>(
      `/api/v1/filesystem${path ? `?path=${encodeURIComponent(path)}` : ""}`,
    ),
  addRootFolder: (mediaType: string, path: string) =>
    request<RootFolder>("/api/v1/rootfolder", json({ mediaType, path })),
  deleteRootFolder: (id: number) =>
    request<void>(`/api/v1/rootfolder/${id}`, { method: "DELETE" }),

  getMetadataSettings: () =>
    request<MetadataSettings>("/api/v1/settings/metadata"),
  saveMetadataSettings: (
    active: string,
    providers: Record<string, ProviderSettings>,
    extra?: {
      fallbacks?: string[];
      comicProvider?: string;
      comicCoverSource?: string;
      language?: string;
      country?: string;
      includeAdult?: boolean;
      includeCompilations?: boolean;
    },
  ) =>
    request<MetadataSettings>("/api/v1/settings/metadata", {
      ...json({ active, providers, ...extra }),
      method: "PUT",
    }),
  testMetadataProvider: (provider: string, settings: ProviderSettings) =>
    request<{ ok: boolean }>("/api/v1/settings/metadata/test",
      json({ provider, settings })),
  clearMetadataCache: () =>
    request<{ removed: number; freedBytes: number }>("/api/v1/settings/metadata/cache", {
      method: "DELETE",
    }),
  clearCoverCache: () =>
    request<{ removed: number; freedBytes: number }>("/api/v1/library/covers/cache", {
      method: "DELETE",
    }),
  clearDescriptions: () =>
    request<{ descriptionsCleared: number }>("/api/v1/settings/metadata/descriptions", {
      method: "DELETE",
    }),
  clearAllCache: () =>
    request<{ removed: number; freedBytes: number; descriptionsCleared: number }>(
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
  ignoreWantedMusicAlbum: (id: number) =>
    request<void>(`/api/v1/music/wanted/${id}/ignore`, { method: "POST" }),
  searchWantedMusicAlbum: (id: number) =>
    request<{ releases: Release[]; errors: string[] }>(`/api/v1/music/wanted/${id}/search`),
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

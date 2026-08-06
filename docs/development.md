# Development

Go 1.25+ backend, React 19 + Vite frontend (Node 22+) — matching
LibriNode/AcerviNode's own stack exactly, embedded into the same binary
via `go:embed`.

```sh
cd web && npm install && npm run build && cd ..   # frontend — only needed after UI changes
go run ./cmd/cantinode                              # starts on http://localhost:7847
go test ./...
go vet ./...
go build ./cmd/cantinode                            # embeds web/dist if present
```

A committed `web/dist/.gitkeep` keeps `go build` working on a fresh clone
even before `npm run build` has ever run — you'll just get an empty `/`
until it has.

Frontend-only iteration (Node 22+):

```sh
cd web
npm install
npm run dev      # Vite dev server on its own port, proxies /api to :7847
npm run build    # production build into web/dist, embedded on the next go build
npm run lint      # oxlint
```

> **Windows note:** developed on Windows day to day; the backend is plain
> Go and builds fine here, and every feature runs the same on Windows as
> on Linux — there's no FUSE/mount dependency anywhere in this project.

## Why `make build`, not `go build`

`go:embed` bakes in whatever's already on disk in `web/dist` *at build
time*, not what's in git — `web/dist`'s actual built files are gitignored
(only `.gitkeep` is committed). A plain `git pull && go build`, with no
frontend step, silently succeeds and produces a real, runnable binary —
just one still serving whatever UI happened to already be sitting in
`web/dist` from an earlier build, no error, nothing to notice until
someone actually looks at the page. `make build` (see `Makefile`) always
does both steps, in the right order, as one command.

## Layout

```
cmd/cantinode/            entrypoint: config, database, scanner, acquisition, HTTP server, background loops
internal/config/           config.yaml + env overrides, defaults/validation
internal/database/          SQLite open + embedded migrations, CRUD for every table
internal/tagreader/          reads ID3v1/v2, FLAC/Vorbis comments, MP4/M4A tags (github.com/dhowden/tag)
internal/tagwriter/          writes ID3v2 (MP3) / Vorbis comments (FLAC) back to a matched file
internal/coverart/            Cover Art Archive client, disk-cached
internal/musicbrainz/        rate-limited MusicBrainz web service client (artist/recording lookup + search)
internal/scanner/             scan -> match -> organize pipeline, tying tagreader+musicbrainz together
internal/prowlarr/             Prowlarr client: indexer search + resolving a release to a magnet URI/file
internal/qbittorrent/          qBittorrent Web API client: torrent add/status (real server or AcerviNode's shim)
internal/sabnzbd/               SABnzbd API client: usenet add/status (real server or AcerviNode's shim)
internal/acquisition/          monitor -> want -> search -> grab -> import, tying prowlarr+qbittorrent+sabnzbd+scanner together
internal/api/                  native versioned REST API (/api/v1), what the UI runs on
web/                              React SPA (embedded via go:embed — see web/webui.go)
docs/                              this documentation
```

## The scan → match → organize pipeline

See `internal/scanner`:

- **Scan** (`scanner.go`) walks each root folder, reads tags via
  `internal/tagreader`, and upserts a `track_files` row per file
  (`upsertFile`) — a rescan never touches an already-matched file's
  `match_status`/`track_id`, only a fresh file starts `unmatched`. As it
  walks, not-yet-matched files are grouped by directory (`filepath.Dir`)
  into `groups map[string][]folderEntry`. Matching itself runs in a
  second pass after the walk completes, one folder-group at a time
  (`matchFolder`) — not inline during the walk.
- **Match** (`matcher.go`, `folder_match.go`): a file whose tags carry a
  MusicBrainz recording ID is looked up directly, independent of its
  folder (`matchFileDirect`) — the same fast path as before this rework.
  Every other file in a folder is matched together against **one**
  MusicBrainz *release* (`matchFolder`/`resolveFolderRelease`): an
  embedded release MBID on any file is used directly if present;
  otherwise the folder's own consistent Artist/Album tags
  (`folderTagConsensus`) drive a `SearchReleases` call, scored by
  MusicBrainz relevance combined with how close a candidate's own track
  count is to the folder's file count (`pickBestReleaseCandidate`) — a
  disambiguator per-file search never had. Each file is then slotted into
  a specific track within that one release (`slotTrack`): disc+track
  number from local tags first, falling back to a hand-rolled
  case/punctuation-insensitive title-similarity score
  (`titlesim.go`) against the release's own already-fetched tracklist — no
  extra network call. Every successful slot still goes through the same
  `Scanner.applyMatch` that existed before this rework (creates/reuses
  `artists`/`albums`/`tracks` rows) — only the track/disc-number
  *arguments* passed to it differ: the release's own authoritative
  position instead of the file's own tags. A folder whose files don't
  agree on Artist/Album, or whose release search/lookup comes back empty,
  falls back to the original independent per-file fuzzy search
  (`matchFileFuzzy`) for just that folder — never a fatal scan error.

  This exists because per-file independent matching could split one
  album folder across several different `albums` rows — different
  pressings of the same release-group (or, worse, an unrelated release)
  whenever per-file fuzzy scores happened to diverge on different tracks.
  Grouping by folder and resolving one release per folder also cuts
  MusicBrainz call volume per folder from O(track count) to O(1-2).

  Known limitation: grouping is one filesystem level deep
  (`filepath.Dir`) — a multi-disc release ripped into `CD1`/`CD2`
  subfolders is currently treated as two separate folders/albums, not
  one.
- **Organize** (`organizer.go`): `FormatPath` renders `naming_format`
  against a matched file's artist/album/track; `OrganizeFile` moves the
  file there, refusing to overwrite an existing destination.

`Scanner.UpdateSettings` lets `internal/api`'s settings endpoint push a
live `naming_format`/`min_match_confidence`/`organize_on_match` change
into a running Scanner without a restart — guarded by its own mutex since
a scan and a settings update can genuinely run concurrently.

## The acquisition pipeline (optional)

See `internal/acquisition`:

- **Monitor** (`monitor.go`): `MonitorArtist` looks an artist up on
  MusicBrainz and seeds `wanted_albums` from their release groups —
  `syncWantedAlbums` only wants `primary-type == "Album"` with no
  secondary types (Live/Compilation/...). `SyncArtist` re-runs this later
  without resetting an already-downloaded/ignored album back to `wanted`.
- **Search** (`search.go`): `SearchReleases` queries `internal/prowlarr`
  with the monitored artist's name plus the wanted album's title.
- **Grab** (`grab.go`): `GrabRelease` resolves the chosen release's
  content via `prowlarr.Client.FetchContent` (a magnet URI, or actual
  `.torrent`/`.nzb` bytes — Prowlarr's own `downloadUrl`/`magnetUrl` are
  already proxied through Prowlarr itself) and hands it to
  `internal/qbittorrent` (torrent) or `internal/sabnzbd` (usenet),
  whichever matches, recording a `downloads` row. Always a human choice —
  there's no code path that grabs a search result without an explicit
  `GrabRelease` call from a user action.
- **Poll + import** (`poll.go`): `PollDownloads` checks every
  `downloading` row against whichever client it was grabbed through; once
  that client reports it done, `importDownload` copies the files from its
  local disk into the target root folder's `_incoming/download-<id>/` and
  runs `Scanner.ScanRootFolder` on it immediately. A failed/vanished
  download reverts its wanted album back to `wanted` (`failDownload`)
  rather than leaving it stuck.

`Service.UpdateClients` swaps in new Prowlarr/qBittorrent/SABnzbd clients
live — called by `internal/api`'s settings endpoint whenever their
connection details change, same pattern as `Scanner.UpdateSettings`. Any
of the three may be `nil`, meaning "not configured" — every method checks
and returns a plain error rather than assuming a client exists.

## Adding a new tag/format source

`internal/tagreader.Read` wraps `github.com/dhowden/tag`, which detects
format from file content, not extension — `tagreader.IsAudioFile` is
purely a fast pre-filter for the directory walk (MP3, FLAC, M4A/M4B/M4P,
OGG/OGA, DSF; not WAV/WMA/Opus-in-Ogg, which the underlying library
doesn't parse tags from). MusicBrainz IDs are normalized out of three
different raw-key shapes (Vorbis comments, ID3v2 TXXX/UFID frames, MP4
freeform atoms) into one lookup table — see
`tagreader.extractMusicBrainzIDs`'s own doc comment for the specifics of
each.

## Releases

Not yet automated — no `.github/workflows/release.yml`, no tagged
binaries (see [Roadmap](../ROADMAP.md)).

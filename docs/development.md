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
internal/acervinode/          AcerviNode client: qBittorrent + SABnzbd compat-shim add/status
internal/acquisition/          monitor -> want -> search -> grab -> import, tying prowlarr+acervinode+scanner together
internal/api/                  native versioned REST API (/api/v1), what the UI runs on
web/                              React SPA (embedded via go:embed — see web/webui.go)
docs/                              this documentation
```

## The scan → match → organize pipeline

See `internal/scanner`:

- **Scan** (`scanner.go`) walks each root folder, reads tags via
  `internal/tagreader`, and upserts a `track_files` row per file. A
  rescan never touches an already-matched file's `match_status`/
  `track_id` — only a fresh file starts `unmatched`.
- **Match** (`matcher.go`): a file whose tags carry a MusicBrainz
  recording ID is looked up directly (`Scanner.matchFile`); otherwise a
  fuzzy search is scored, and only accepted above `min_match_confidence`.
  Matching creates/reuses `artists`/`albums`/`tracks` rows as needed
  (`Scanner.applyMatch`) — track/disc number come from the file's own
  tags, not MusicBrainz (a Recording alone doesn't carry a release-scoped
  track position).
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
  `internal/acervinode`, recording a `downloads` row. Always a human
  choice — there's no code path that grabs a search result without an
  explicit `GrabRelease` call from a user action.
- **Poll + import** (`poll.go`): `PollDownloads` checks every
  `downloading` row against AcerviNode; once AcerviNode reports it done,
  `importDownload` copies the files from AcerviNode's own local disk into
  the target root folder's `_incoming/download-<id>/` and runs
  `Scanner.ScanRootFolder` on it immediately. A failed/vanished download
  reverts its wanted album back to `wanted` (`failDownload`) rather than
  leaving it stuck.

`Service.UpdateClients` swaps in new Prowlarr/AcerviNode clients live —
called by `internal/api`'s settings endpoint whenever their connection
details change, same pattern as `Scanner.UpdateSettings`. Either (or both)
may be `nil`, meaning "not configured" — every method checks and returns a
plain error rather than assuming a client exists.

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

# Development

Go 1.25+ backend, React 19 + Vite frontend, SQLite (pure Go, no cgo).

```sh
go run ./cmd/cantinode     # starts on http://localhost:7847
go test ./...
go build ./cmd/cantinode   # embeds web/dist if present
```

Frontend (Node 22+):

```sh
cd web
npm install
npm run dev      # Vite dev server, proxies /api to :7847
npm run build    # production build into web/dist
```

> **Windows note:** official Windows builds are on hold for now (see the
> [roadmap](../ROADMAP.md)), but the backend is plain Go and builds fine on
> Windows for local development. With Smart App Control enabled, Windows
> blocks locally compiled (unsigned) binaries — develop inside WSL or
> disable SAC.

## Layout

```
cmd/cantinode/         entrypoint; wires up and runs the background loops
                       (health check, Completed Download Handling, the
                       wanted-list sweep, discography refresh, metadata
                       backfill, import-list sync, Plex playlist sync);
                       restore staging
internal/api/          REST handlers, router, auth, backups
internal/musiclibrary/ domain model + SQLite store (artists/albums/tracks)
internal/musicscanner/ file scanning, MusicBrainz matching, organize/rename
                       (library-wide and per-album)
internal/importer/     Completed Download Handling: polls in-flight grabs,
                       copies a finished one's audio files into the
                       library, scans them in
internal/autosearch/   wanted-list sweep: searches and grabs for every
                       monitored artist's still-wanted albums, on a timer
internal/discography/  caches an artist's own discography from
                       MusicBrainz - the cheap "did anything new appear"
                       half of a full artist refresh
internal/discoveryrefresh/ periodic sweep re-caching every monitored
                       artist's discography, so a new release lands in
                       Missing without a manual "Refresh metadata"
internal/metadatabackfill/ fills in discography/bio/photo for an artist
                       that only ever got a bare name/MBID row - restart-
                       safe backstop for the same work a scan or "Monitor"
                       already does inline, on its own periodic timer
internal/importlist/   periodic sync resolving every enabled import list
                       (a MusicBrainz Series, a plain artist list, or a
                       Last.fm user/tag) to add+monitor new artists
internal/musicbrainz/  MusicBrainz API client (artist/release lookup+search)
internal/audiodb/      TheAudioDB client (artist bio/photo)
internal/lastfm/       Last.fm client (a user's/tag's top artists, for
                       import lists)
internal/plex/         Plex Media Server client + best-effort "refresh
                       this path" notification on file changes
internal/plexplaylistsync/ two-way playlist sync with a linked Plex
                       server, on its own periodic timer (plus an
                       immediate pass right after a CantiNode-side edit)
internal/coverart/     Cover Art Archive client + local album-art cache
internal/tagreader/    reads embedded audio tags (MBIDs, title, track#)
internal/tagwriter/    rewrites embedded audio tags (the standalone
                       "write tags" action — not part of organize)
internal/indexer/      Newznab/Torznab clients, search fan-out, backoff;
                       native-source registry
  prowlarr/            native source: searches a Prowlarr instance's own
                       API directly instead of per-indexer duplication
internal/release/      release parsing + scoring
internal/candidatesearch/ shared search->score->rank pipeline used by both
                       manual search/upgrade-search and the autosearch sweep
internal/download/     qBittorrent/SABnzbd/direct clients, grabs, blocklist
internal/relname/      generic release-name text utilities (used by
                       release scoring and download-queue enrichment)
internal/library/      generic shared model: root folders, quality profiles
internal/imagecache/   provider-image download cache
internal/health/       background health checks
internal/redact/       strips credential-shaped values out of errors/logs
internal/logging/      rotating log file
internal/config/       config.yaml + env overrides
internal/database/     SQLite open + embedded migrations
web/                   React SPA (embedded via go:embed)
docs/                  this documentation (mkdocs)
packaging/             systemd unit
```

Releases are cut by tagging `v*` — CI builds version-stamped binaries
(linux amd64/arm64) and attaches them to a GitHub release. See
`.github/workflows/release.yml`. Docker and Windows builds are on hold for
now (see the [roadmap](../ROADMAP.md)).

Docs preview: `pip install mkdocs-material && mkdocs serve`.

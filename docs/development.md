# Development

Go 1.25+ backend, React 19 + Vite frontend, SQLite (pure Go, no cgo).

```sh
go run ./cmd/librinode     # starts on http://localhost:7845
go test ./...
go build ./cmd/librinode   # embeds web/dist if present
```

Frontend (Node 22+):

```sh
cd web
npm install
npm run dev      # Vite dev server, proxies /api to :7845
npm run build    # production build into web/dist
```

> **Windows note:** official Windows builds are on hold for now (see the
> [roadmap](../ROADMAP.md)), but the backend is plain Go and builds fine on
> Windows for local development. With Smart App Control enabled, Windows
> blocks locally compiled (unsigned) binaries — develop inside WSL or
> disable SAC.

## Layout

```
cmd/librinode/        entrypoint, background loops, restore staging
internal/api/         REST handlers, router, auth, backups
internal/library/     domain model + SQLite store (authors/books/series)
internal/metadata/    provider registry + fallback chain; hardcover/,
                      anilist/, comicvine/, openlibrary/, googlebooks/
internal/indexer/     Newznab/Torznab clients, search fan-out, backoff;
                      native-source registry + audiobookbay/, libgen/
internal/release/     release parsing + scoring
internal/download/    qBittorrent/SABnzbd/direct clients, grabs, blocklist
internal/autosearch/  wanted-list sweeps, per-book search
internal/importer/    Completed Download Handling, seed-goal cleanup
internal/refresh/     scheduled + manual metadata re-sync
internal/scanner/     library file scanning + matching
internal/organize/    naming-template rename engine (all media types)
internal/naming/      template token rendering
internal/opf/         OPF sidecar rendering
internal/comicinfo/   ComicInfo.xml for CBZ archives
internal/comiccover/  cover extraction from CBZ/CBR archives
internal/imagecache/  provider-image download cache
internal/health/      background health checks
internal/redact/      strips credential-shaped values out of errors/logs
internal/logging/     rotating log file
internal/config/      config.yaml + env overrides
internal/database/    SQLite open + embedded migrations
web/                  React SPA (embedded via go:embed)
docs/                 this documentation (mkdocs)
packaging/            systemd unit
```

Releases are cut by tagging `v*` — CI builds version-stamped binaries
(linux amd64/arm64) and attaches them to a GitHub release. See
`.github/workflows/release.yml`. Docker and Windows builds are on hold for
now (see the [roadmap](../ROADMAP.md)).

Docs preview: `pip install mkdocs-material && mkdocs serve`.

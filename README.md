<div align="center">

# 🖋️ LibriNode

**Self-hosted automation for written media — ebooks, audiobooks, manga, comics, and magazines.**

An alternative in the *arr tradition: monitor what you want, search your indexers, hand releases to your download client, and import everything into clean, reader-ready libraries — automatically.

[![Release](https://img.shields.io/github/v/release/obstruct-exit-emit/LibriNode?include_prereleases&label=release)](https://github.com/obstruct-exit-emit/LibriNode/releases)
[![CI](https://github.com/obstruct-exit-emit/LibriNode/actions/workflows/ci.yml/badge.svg)](https://github.com/obstruct-exit-emit/LibriNode/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

> 🚧 **Pre-1.0, but feature-complete.** All five media types work end to end — metadata search through automatic grabbing to organized imports. What remains before 1.0 is hardening: real-world burn-in. See the [roadmap](ROADMAP.md).

---

## Why LibriNode?

LibriNode is an **alternative** to tools like Readarr (books; development has ended), LazyLibrarian (books and magazines), and Mylar (comics), with a different scope: **all five written-media types managed in one app**, in the familiar *arr style. It sits alongside readers like Kavita, Komga, Calibre, and Audiobookshelf — it feeds them organized libraries rather than replacing them.

## Features

**📚 Five independent libraries** — each media type gets its own root folders, naming templates, quality profiles, and monitoring. Plex-style: a library appears only once you create it.

| Library | Metadata | Formats |
|---|---|---|
| Ebooks | Hardcover, + Open Library / Google Books fallbacks | epub, mobi, azw3, pdf |
| Audiobooks | Hardcover, + fallbacks | m4b, m4a, mp3, flac, opus |
| Manga | AniList (no key) or Hardcover | cbz, cbr, epub |
| Comics | Hardcover or ComicVine | cbz, cbr, pdf |
| Magazines | Provider-less, added by name *(organize-only today)* | pdf, epub, cbz |

**🔍 One acquisition pipeline**
- **Prowlarr application sync** — add LibriNode as a *Readarr* app and Prowlarr pushes its indexers automatically; manual Newznab/Torznab entry works too
- **Native indexers** for sources Prowlarr can't reach (AudioBook Bay, Library Genesis) — built-in, off by default, user-enabled
- Release parsing and scoring that understands formats, retail editions, narrators, volume ranges, and whole-series packs
- Quality profiles with upgrade handling, a failed-release blocklist, and per-indexer failure backoff

**⬇️ Three download protocols**
- **qBittorrent** (torrents) and **SABnzbd** (usenet) — category-scoped, seed-goal aware, debrid-bridge compatible (Real-Debrid/TorBox)
- A built-in **direct fetcher** for plain-HTTP sources — mirror failover, no external program
- Completed Download Handling: finished grabs import, rename, and organize themselves; remote path mappings for clients on other machines

**🏷️ Reader-ready output**
- Audiobookshelf folder layouts with `metadata.opf`; Kavita/Komga layouts with `ComicInfo.xml`; OPF sidecars for Calibre
- Smart scanning: ISBN/ASIN identifier matching (filename + embedded epub metadata), exact title matching, and fuzzy suggestions for everything else
- Multi-book pack imports, colorized/monochrome manga variants, multi-file audiobooks as single units

**🖥️ A modern web UI**
- Poster-grid libraries with detail pages, per-author/series **Missing** sections, per-library **Wanted** cards, a release **Calendar**, and live **Activity**
- Multi-user login with **admin/member roles**, enforced by the backend; first-run setup wizard
- Health checks with self-explaining banners, scheduled backups with staged restore, a built-in log viewer

## Quick start

Grab a binary from [Releases](https://github.com/obstruct-exit-emit/LibriNode/releases) (Linux amd64/arm64) — it's a single self-contained file, UI included. A systemd unit ships in the archive.

> Docker and Windows builds are on hold for now (see the [roadmap](ROADMAP.md)) — planned to return later.

Then open `http://localhost:7845` — a first-run wizard walks you through your account, libraries, metadata, an indexer, and a download client. Full steps: [Installation](docs/installation.md) · [Quickstart](docs/quickstart.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | Linux, from source |
| [Quickstart](docs/quickstart.md) | First-run walkthrough |
| [Libraries](docs/libraries.md) | How each of the five libraries behaves |
| [Acquisition](docs/acquisition.md) | Indexers, native sources, scoring, download clients |
| [Configuration](docs/configuration.md) | config.yaml, auth & roles, naming, backups, HTTPS |
| [API](docs/api.md) | The full REST API — everything the UI does is scriptable |
| [Development](docs/development.md) | Building, layout, contributing |
| [Roadmap](ROADMAP.md) | Development history and what's next |

## Architecture

- **Backend:** Go — one self-contained binary per OS, no runtime dependencies
- **Frontend:** React (Vite), embedded in the binary, served on one port
- **Database:** SQLite (pure Go, no cgo) with embedded, tested migrations
- **API:** versioned REST (`/api/v1`) with API-key auth — the same API the UI uses; Prowlarr-compatible surface for app sync
- **Default port:** `7845` · **License:** GPL-3.0

## Security

Optional login accounts with **admin/member roles** (members get everyday use, not server configuration — enforced server-side), sessions bound to their accounts, PBKDF2-hashed passwords, constant-time API-key checks, and credential redaction in every error and log line. For remote access, run behind a TLS reverse proxy — examples in the [configuration docs](docs/configuration.md#https--reverse-proxies).

## Development

```sh
cd web && npm install && npm run build && cd ..   # frontend (Node 22+)
go build ./cmd/librinode                          # backend  (Go 1.25+)
./librinode                                       # http://localhost:7845
```

`go test ./...` runs the full suite. See [Development](docs/development.md) for the package layout, docs preview, and the Windows Smart-App-Control note.

## License

[GPL-3.0](LICENSE) — the same family as Sonarr, Radarr, and Prowlarr.

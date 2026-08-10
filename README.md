<div align="center">

# 🖋️ CantiNode

**Self-hosted automation for your music library.**

An alternative in the *arr tradition: monitor artists you want, search your indexers, hand releases to your download client, and match/organize everything into a clean, tagged music library — automatically.

[![Release](https://img.shields.io/github/v/release/obstruct-exit-emit/CantiNode?include_prereleases&label=release)](https://github.com/obstruct-exit-emit/CantiNode/releases)
[![CI](https://github.com/obstruct-exit-emit/CantiNode/actions/workflows/ci.yml/badge.svg)](https://github.com/obstruct-exit-emit/CantiNode/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

> 🚧 **Pre-1.0, but feature-complete.** Metadata search through automatic grabbing to matched, organized imports all work end to end. What remains before 1.0 is hardening: real-world burn-in. See the [roadmap](ROADMAP.md).

---

## Why CantiNode?

CantiNode is an **alternative** to tools like Lidarr, in the familiar *arr style, focused entirely on music. It sits alongside players/servers like Navidrome and Plex — it feeds them a matched, tagged, organized library rather than replacing them.

## Features

**🎵 One library, done well** — a Plex-style library that appears once you point a root folder at your music.

| Library | Metadata | Formats |
|---|---|---|
| Music | MusicBrainz (artist/album/track identity) + TheAudioDB (bio/photo) | flac, mp3, m4a, opus, wav |

**🔍 One acquisition pipeline**
- **Prowlarr application sync** — add CantiNode as a *Readarr* app and Prowlarr pushes its indexers automatically; manual Newznab/Torznab entry works too
- Release parsing and scoring that understands formats and retail editions
- Quality profiles with upgrade handling, a failed-release blocklist, and per-indexer failure backoff

**⬇️ Three download protocols**
- **qBittorrent** (torrents) and **SABnzbd** (usenet) — category-scoped, seed-goal aware, debrid-bridge compatible (Real-Debrid/TorBox)
- A built-in **direct fetcher** for plain-HTTP sources — mirror failover, no external program

**🏷️ Tag-matched, organized output**
- Every scanned file is matched against MusicBrainz — by embedded `MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID` tags first (exact), then whole-folder release matching, then fuzzy title search
- Matched files are organized into a configurable `{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}` layout, and can have their tags rewritten to match
- Artist bio/photo (TheAudioDB) and album art (Cover Art Archive) are cached automatically the first time an artist is added — never re-fetched just from browsing

**🖥️ A modern web UI**
- A poster-grid artist library with per-artist detail pages — owned albums (grouped by type: Album/EP/Single/Live/Compilation/…), a **Missing** section for the rest of the discography, and a **Wanted** section for what's queued
- Multi-user login with **admin/member roles**, enforced by the backend; first-run setup wizard
- Health checks with self-explaining banners, scheduled backups with staged restore, a built-in log viewer

## Quick start

Grab a binary from [Releases](https://github.com/obstruct-exit-emit/CantiNode/releases) (Linux amd64/arm64) — it's a single self-contained file, UI included. A systemd unit ships in the archive.

> Docker and Windows builds are on hold for now (see the [roadmap](ROADMAP.md)) — planned to return later.

Then open `http://localhost:7845` — a first-run wizard walks you through your account, your music folder, an indexer, and a download client. Full steps: [Installation](docs/installation.md) · [Quickstart](docs/quickstart.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | Linux, from source |
| [Quickstart](docs/quickstart.md) | First-run walkthrough |
| [Libraries](docs/libraries.md) | How the music library behaves |
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
go build ./cmd/cantinode                          # backend  (Go 1.25+)
./cantinode                                       # http://localhost:7845
```

`go test ./...` runs the full suite. See [Development](docs/development.md) for the package layout, docs preview, and the Windows Smart-App-Control note.

## License

[GPL-3.0](LICENSE) — the same family as Sonarr, Radarr, and Prowlarr.

<div align="center">

# 🎵 CantiNode

**A self-hosted music library organizer.**

CantiNode scans folders of music you already have, matches every artist,
album, and track against MusicBrainz, and organizes the files into a
consistent layout — a Lidarr-shaped problem tackled from the organization
side first, with acquisition (indexers, download clients) and richer
playlisting layered on afterward.

[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

> 🚧 **Pre-1.0, organizer-first.** Root folders, library scanning, MusicBrainz
> matching, manual review of unmatched files, and file organization
> (rename/move by a configurable naming scheme) all work end to end. No
> indexer/download-client acquisition pipeline yet, no LLM playlists, no Plex
> sync yet — see the [roadmap](ROADMAP.md).

---

## Why CantiNode?

If you already run [LibriNode](https://github.com/obstruct-exit-emit/LibriNode)
for your book library or [AcerviNode](https://github.com/obstruct-exit-emit/AcerviNode)
for your debrid downloads, CantiNode is their sibling for your music library:
same single self-contained Go binary, same operator experience, same look —
built by the same author, in the same style, on purpose.

Where Lidarr treats organization as a side effect of acquisition, CantiNode
starts from the opposite end: point it at folders of music you already have
(ripped, purchased, downloaded some other way) and it builds a real library
out of them — matched to MusicBrainz, browsable, correctly named — without
requiring an indexer or a download client to get there.

## Features

**📁 Root folders & scanning**

- Add one or more root folders; CantiNode walks them for audio files
  (MP3, FLAC, M4A/AAC, OGG, Opus, WAV) and reads embedded tags
  (ID3v1/v2, Vorbis comments, MP4 atoms) — pure Go, no external decoder
  or cgo dependency.
- A background scan runs on a configurable interval; a scan can also be
  triggered on demand from the API or the UI.

**🎯 MusicBrainz matching**

- A file whose tags already carry MusicBrainz IDs (common — Picard and
  most rippers embed these) is matched directly, high confidence,
  automatically.
- Otherwise, CantiNode fuzzy-searches MusicBrainz by artist/album/track
  title and only accepts a match above a confidence threshold — anything
  less confident is left for manual review instead of guessing.
- Unmatched files get their own review queue in the UI: search
  MusicBrainz yourself and link a file by hand.

**🗂️ Organization**

- A configurable naming format (default
  `{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}`) renames and
  moves matched files into a consistent layout.
- Preview before you commit — see exactly what will move where — and it
  never silently overwrites an existing file.

**🖥️ Native API + web UI**

- Versioned REST API (`/api/v1`), API-key authenticated — root folders,
  library browsing, unmatched-file review, scan control, organize
  preview/apply, settings.
- A React (Vite) single-page dashboard, embedded into the binary.

## Quick start

```sh
go build ./cmd/cantinode
./cantinode
```

Then open `http://localhost:7847`. Full steps:
[Installation](docs/installation.md) · [Quickstart](docs/quickstart.md).

## Documentation

| | |
|---|---|
| [Installation](docs/installation.md) | From source |
| [Quickstart](docs/quickstart.md) | First-run walkthrough |
| [Configuration](docs/configuration.md) | config.yaml, ports, naming |
| [API](docs/api.md) | The native `/api/v1` — everything the web UI does is scriptable |
| [Development](docs/development.md) | Building, layout, contributing |
| [Roadmap](ROADMAP.md) | Development history and what's next |

## Architecture

- **Backend:** Go — one self-contained binary, no runtime dependencies
- **Database:** SQLite (pure Go, no cgo), embedded migrations
- **Matching:** `internal/musicbrainz` (rate-limited MusicBrainz client) +
  `internal/scanner` (scan → match → organize pipeline)
- **API:** `internal/api`, versioned `/api/v1`
- **Frontend:** React (Vite) SPA embedded via `go:embed` — same look as
  LibriNode and AcerviNode

## License

GPL-3.0 — see [LICENSE](LICENSE).

# 🎵 CantiNode Roadmap

Where the project has been and where it's going. The fine-grained record of
every change lives in the [CHANGELOG](CHANGELOG.md).

**Legend:** ✅ complete · 🔄 in progress · 💡 under consideration · ⏳ blocked

## At a glance

| Phase | Scope | Status |
|---|---|---|
| [0 — Foundation](#phase-0--foundation-) | Repo, config, database, CI, shared design system | ✅ |
| [1 — Organizer vertical slice](#phase-1--organizer-vertical-slice-) | Scan, MusicBrainz matching, organize, native API, web UI | ✅ |
| [2 — Tag writing](#phase-2--tag-writing-) | Fix incorrect/missing tags on disk, not just read them | 💡 |
| [3 — Cover art](#phase-3--cover-art-) | Cover Art Archive lookup + embedding/caching | 💡 |
| [4 — Acquisition pipeline](#phase-4--acquisition-pipeline-) | Indexers (Newznab/Torznab), monitored artists/albums, wanted/missing, download client polling (incl. AcerviNode) | 💡 |
| [5 — LLM playlists](#phase-5--llm-playlists-) | LLM-generated playlists from natural-language prompts | 💡 |
| [6 — Plex sync](#phase-6--plex-sync-) | Push playlists to Plex; trigger a Plex library scan of paths CantiNode organized | 💡 |

---

## Phase 0 — Foundation ✅

Repo scaffold matching LibriNode/AcerviNode conventions: `go.mod`, GPL-3.0
license, `Makefile` (frontend-then-backend build), CI (`make build` → `go vet`
→ `go test`), `config.yaml` + `CANTINODE_*` env overrides via a
`Load`/`Validate`/`Save` config package, SQLite (pure Go, no cgo) with
embedded ordered migrations, and the shared design system (`web/src/index.css`
design tokens ported from AcerviNode so all three apps look like one family).

## Phase 1 — Organizer vertical slice ✅

- Root folders (CRUD via API/UI).
- `internal/tagreader`: reads ID3v1/v2, FLAC/Vorbis comments, MP4/M4A, OGG
  tags from scanned files.
- `internal/musicbrainz`: rate-limited (1 req/sec, per MusicBrainz's usage
  policy) client for MBID lookup and fuzzy artist/release/recording search.
- `internal/scanner`: walks root folders, upserts `track_files`, matches
  each file (MBID-direct where tags already carry one, else fuzzy search
  above a confidence threshold, else left `unmatched` for manual review).
- Organize: configurable naming format renames/moves matched files,
  preview-before-apply, never overwrites.
- `internal/api`: versioned `/api/v1`, API-key auth — root folders, library
  browse, unmatched review + manual match, scan trigger/status, organize
  preview/apply, settings.
- Web UI: Root Folders, Library (artist → album → tracks), Unmatched
  (review + manual match), Scan status, Settings.

## Phase 2 — Tag writing 💡

Once a file is matched, write the corrected/completed metadata back into its
own tags (not just CantiNode's database) — the same "fix the actual file"
value beets provides. Deliberately deferred out of Phase 1: needs a
per-format writer (ID3v2 is well-trodden in Go; FLAC/Vorbis-comment and
MP4/M4A writing are each their own scope), and touching a user's original
file's tag data is a much higher-stakes operation than reading it, worth
its own careful pass rather than folding into the read-only scan.

## Phase 3 — Cover art 💡

Cover Art Archive lookup keyed off the release MBID already stored once an
album is matched; cache art locally, surface it in the Library UI.

## Phase 4 — Acquisition pipeline 💡

The Lidarr-parity piece deliberately left out of v1 (see the initial scoping
decision, Phase 1): Newznab/Torznab indexer search, monitored
artists/albums with a wanted/missing list, and download-client polling —
AcerviNode first (same author, same API conventions, same "speaks a
download-client protocol so an *arr-shaped app can add to it" role it
already plays for Sonarr/Radarr), generic qBittorrent/SABnzbd after.

## Phase 5 — LLM playlists 💡

Requested directly, for after the core is running: generate playlists from a
natural-language prompt against the actual library CantiNode has matched and
organized (not a generic recommendation — grounded in what's really on disk),
using an LLM. Needs a provider choice (Claude API key at minimum) and a
playlist data model.

## Phase 6 — Plex sync 💡

Requested directly, alongside Phase 5: push a CantiNode-generated (or
manually built) playlist to a configured Plex server, and — since CantiNode
is the thing moving/renaming files on disk — trigger a targeted Plex library
scan of the specific paths it just organized, instead of relying on Plex's
own periodic full scan to notice.

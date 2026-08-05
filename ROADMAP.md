# 🎵 CantiNode Roadmap

Where the project has been and where it's going. The fine-grained record of
every change lives in the [CHANGELOG](CHANGELOG.md).

**Legend:** ✅ complete · 🔄 in progress · 💡 under consideration · ⏳ blocked

## At a glance

| Phase | Scope | Status |
|---|---|---|
| [0 — Foundation](#phase-0--foundation-) | Repo, config, database, CI, shared design system | ✅ |
| [1 — Organizer vertical slice](#phase-1--organizer-vertical-slice-) | Scan, MusicBrainz matching, organize, native API, web UI | ✅ |
| [2 — Tag writing](#phase-2--tag-writing-) | Fix incorrect/missing tags on disk, not just read them | ✅ |
| [3 — Cover art](#phase-3--cover-art-) | Cover Art Archive lookup + embedding/caching | ✅ |
| [4 — Acquisition pipeline](#phase-4--acquisition-pipeline-) | Prowlarr search, monitored artists/wanted albums, manual grab via AcerviNode, auto-import | ✅ |
| [5 — LLM playlists](#phase-5--llm-playlists-) | LLM-generated playlists from natural-language prompts | 💡 |
| [6 — Plex sync](#phase-6--plex-sync-) | Push playlists to Plex; trigger a Plex library scan of paths CantiNode organized | 💡 |
| [7 — Acquisition hardening](#phase-7--acquisition-hardening-) | Quality profiles, auto-grab, MP4/M4A tag writing, multi-root-folder targeting | 💡 |

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
- ✅ **Nil-slice-to-null JSON bug found and fixed post-launch** — every
  `List*` method in `internal/database` returned Go's nil slice for an
  empty result, which `json.Marshal` encodes as `null` instead of `[]`.
  On a fresh install with an empty library, this crashed the web UI right
  after the API key gate succeeded (`artists.length` on `null`). Found by
  actually driving the built UI with a headless browser rather than
  re-reading the code, fixed across every affected method, and covered
  with a regression test per method.

## Phase 2 — Tag writing ✅

`internal/tagwriter` embeds a matched file's corrected metadata (artist/
album/track, MusicBrainz IDs) back into its own tags — MP3 (a hand-rolled
ID3v2.3 writer, built on the same byte-level knowledge as
`internal/tagreader`'s own tests) and FLAC (Vorbis comments via
`go-flac`/`flacvorbis`, preserving every other existing field and metadata
block untouched) — both written to a temp file and renamed over the
original, atomic and Windows-safe. Manual only (a "Write tags" action per
file in the Library UI), not automatic on match, matching the same
cautious-by-default posture as `organize_on_match`. MP4/M4A and OGG
writing are a known gap — correctly rewriting MP4's nested atom offset
tables is materially riskier to get wrong than ID3v2/FLAC, and not worth
that risk for v1 (see Phase 7).

## Phase 3 — Cover art ✅

`internal/coverart` fetches a release's front cover (`-250` thumbnail) from
Cover Art Archive, keyed by the release MBID already stored on a matched
album, and disk-caches it — including caching a confirmed "no cover art"
404 as its own sentinel, so a release without art isn't re-fetched on every
page load. Served via `GET /api/v1/albums/{id}/cover`, which (uniquely
among CantiNode's routes) accepts the API key as a `?api_key=` query
parameter alongside the usual `Authorization` header, since a plain HTML
`<img src>` can't attach custom headers — the same relaxation the
*arr-ecosystem convention makes for image endpoints specifically, not the
API broadly. Shown in the Library album grid, hidden entirely (no
broken-image icon) rather than shown broken when a release has none.

## Phase 4 — Acquisition pipeline ✅

The Lidarr-parity piece deliberately left out of v1 (see the initial
scoping decision, Phase 1) — built organizer-first, then added on request:

- **Monitor an artist**: search MusicBrainz by name, monitor by MBID.
  Seeds "wanted" albums from the artist's release groups — plain studio
  albums only (`primary-type == "Album"`, no secondary types like
  Live/Compilation) — with a manual re-sync to pick up new releases later.
  Existing wanted-album status is never reset by a re-sync.
- **Search**: `internal/prowlarr` — `GET /api/v1/search` against a
  self-hosted Prowlarr instance, scoped to the Music category. Grabbing
  goes through CantiNode's own AcerviNode client directly
  (`FetchContent` resolves a release's link into either a real magnet URI
  or the actual `.torrent`/`.nzb` bytes, following Prowlarr's own proxy
  redirects itself), not Prowlarr's own "send to configured download
  client" endpoint — CantiNode owns the AcerviNode relationship the same
  way it owns MusicBrainz and Cover Art Archive, independent of whatever
  download client Prowlarr's own settings might separately have
  configured.
- **Grab is manual only** — confirmed directly with the user before
  building this: CantiNode never auto-downloads a search result. v1 has
  no quality-profile system, so "auto-grab" would just mean "take
  whichever result Prowlarr's own relevance ranking put first, unreviewed"
  — real downloads happening unsupervised on that basis was judged not
  worth it. A human always picks the release.
- `internal/acervinode` — a client for AcerviNode's qBittorrent and
  SABnzbd compat shims (the same protocols Sonarr/Radarr already use
  against it), built directly against its documented contracts
  (`docs/qbittorrent-api.md`/`docs/sabnzbd-api.md` in the AcerviNode repo):
  session-cookie login for the qBittorrent shim (with automatic re-login
  on a 403 — AcerviNode's own sessions expire after 24h) and
  per-request `apikey` for the SABnzbd shim. Adds under the `music`
  category, which AcerviNode pre-registers automatically as Lidarr's own
  well-known default — no separate setup step needed on the AcerviNode
  side.
- **Import**: a background poll (every 2 minutes, independent of the
  library scan interval) checks every in-flight download's status; once
  AcerviNode reports it done, CantiNode copies (never moves — AcerviNode
  keeps its own copy under its own retention policy) the files from
  AcerviNode's local disk into the target root folder's `_incoming/`
  subfolder, then runs the normal scan/match pipeline on them immediately.
  A failed download reverts its wanted album back to "wanted" rather than
  leaving it stuck, so the user can just try a different release.
- Web UI: a new Wanted tab — monitored artists, per-artist wanted-album
  list with status badges, a release-search-and-grab dialog, and a live
  downloads-activity view.
- Both Prowlarr and AcerviNode are entirely optional — a fresh install has
  neither configured, and every acquisition call reports a plain "not
  configured" error rather than the feature being reachable at all in a
  broken state.

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

## Phase 7 — Acquisition hardening 💡

Follow-on refinements to Phase 4, deliberately left out of the first pass:

- **Quality profiles / auto-grab** — a real decision engine (format/bitrate
  preference, minimum seeders, release-title parsing) that would make
  unattended auto-grab a reasonable default instead of the "take the
  indexer's top-ranked result, unreviewed" shortcut it would otherwise be.
- **Per-artist/per-grab root folder targeting** — v1 always imports into
  the first configured root folder; multiple libraries (e.g. by genre or
  quality tier) would need a real destination choice.
- **MP4/M4A and OGG tag writing** — see Phase 2's own note on why this was
  scoped out initially (MP4 atom offset tables are risky to get wrong).
- **Torrent-file infohash without the diff heuristic** — `AddTorrentFile`
  currently identifies a just-added torrent by snapshotting AcerviNode's
  category listing before/after rather than computing the infohash
  directly from the uploaded bencoded data (would need a small bencode
  parser + SHA1), which is reliable for CantiNode's one-grab-at-a-time
  usage but not under concurrent adds from elsewhere.

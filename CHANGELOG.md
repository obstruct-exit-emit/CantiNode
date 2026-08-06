# Changelog

## Unreleased

### Added

- Initial project scaffold: Go module, GPL-3.0 license, Makefile, CI,
  config package, SQLite database with embedded migrations, shared design
  system ported from AcerviNode.
- Root folders, library scanning, MusicBrainz matching (MBID-direct and
  fuzzy search), manual review of unmatched files, and file organization
  (configurable naming, preview/apply).
- Versioned native REST API (`/api/v1`) and an embedded React web UI.
- Tag writing: embed a matched file's corrected metadata back into its own
  ID3v2 (MP3) or Vorbis comment (FLAC) tags, via a new "Write tags" action
  per file — see `internal/tagwriter`.
- Cover art: fetch and disk-cache a matched album's front cover from Cover
  Art Archive, shown in the Library album grid — see `internal/coverart`.
- Acquisition pipeline (all optional, off by default, and independently
  configurable): monitor an artist by MusicBrainz search, auto-want their
  studio albums, search a self-hosted Prowlarr instance for releases, and
  grab one (manual only, no auto-grab) directly through a qBittorrent
  and/or SABnzbd connection — a genuine standalone instance of either, or
  [AcerviNode](https://github.com/obstruct-exit-emit/AcerviNode), which
  exposes compat shims for both — see `internal/acquisition`,
  `internal/prowlarr`, `internal/qbittorrent`, `internal/sabnzbd`, and the
  new Wanted tab. A background poll imports a finished download into the
  library automatically once its download client reports it done.
- Root folder picker: a "Browse..." button in Root Folders opens a
  server-side directory browser (`GET /api/v1/browse-directories`) instead
  of requiring an exact path typed by hand.
- Cancel a grab: `DELETE /api/v1/downloads/{id}` removes it from its
  download client and reverts the wanted album back to `wanted`.
- Release picker shows seeders/peers for every torrent result and sinks
  0-seeder ("dead") torrents to the bottom, disabled from being grabbed.
- Unmatch and Delete actions for track files, in both the Library and
  Unmatched views — Unmatch reverts to unmatched (file untouched), Delete
  removes the file from disk and its own row.

### Fixed

- Every `List*` method in `internal/database` returned Go's nil slice for
  an empty result set, which `json.Marshal` encodes as `null` — crashing
  the web UI (`artists.length` on `null`) on a fresh install with an empty
  library, right after the API key gate succeeded. Found by driving the
  built UI with a headless browser rather than re-reading the code; fixed
  across every affected method, with a regression test per method.
- A fresh install minted a random API key on every process start but
  never wrote it to `config.yaml`, so restarting before ever touching
  Settings silently rotated the key each time. `config.Load` now persists
  a freshly generated key immediately.
- MusicBrainz search results polluted by rip/format junk in a file's own
  Album tag (e.g. "... SHM-CD", "... 24-96 hdtracks") — CantiNode now
  strips known format/rip tokens before querying, in both automatic
  matching and the manual "Search MusicBrainz" action.
- Whole-album (release-based) matching, reworked: matching was originally
  per-file/independent (`SearchRecordings` per track, no awareness of
  sibling files), which could split a single album folder across several
  different `albums` rows — found in a real library where one 14-track
  folder ended up split across three different MusicBrainz releases of
  the same release-group, plus one track matched to an entirely unrelated
  release. Files are now grouped by directory and resolved against one
  MusicBrainz release per folder (an embedded release MBID if any file
  carries one, else a release search scored by relevance + track-count
  closeness to the folder's file count), then slotted into that release's
  own tracklist — cutting MusicBrainz call volume per folder from
  O(track count) to O(1-2) as a side effect. See `internal/scanner/folder_match.go`.

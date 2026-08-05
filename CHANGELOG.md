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

### Fixed

- Every `List*` method in `internal/database` returned Go's nil slice for
  an empty result set, which `json.Marshal` encodes as `null` — crashing
  the web UI (`artists.length` on `null`) on a fresh install with an empty
  library, right after the API key gate succeeded. Found by driving the
  built UI with a headless browser rather than re-reading the code; fixed
  across every affected method, with a regression test per method.

# CantiNode

A self-hosted **music library** automation server — an alternative to
Lidarr.

CantiNode monitors artists you want, searches your indexers, sends releases
to your download client, then matches scanned files against MusicBrainz and
organizes them — automatically.

> 🚧 CantiNode is **pre-1.0**. The whole loop works end-to-end, but expect
> rough edges and breaking changes until 1.0.

## The library

Music is the only library. It's Plex-style: it appears in the UI once you
add a root folder for it.

| Type | Metadata | Formats |
|---|---|---|
| Music | MusicBrainz (artist/album/track identity) + TheAudioDB (bio/photo) | flac, mp3, m4a, opus, wav |

## Highlights

- One acquisition pipeline: Newznab/Torznab indexers (or Prowlarr sync),
  release parsing and scoring, quality profiles with upgrades, qBittorrent
  and SABnzbd.
- A built-in **direct** HTTP fetcher for plain-HTTP sources, alongside
  Newznab/Torznab. See [Acquisition](acquisition.md).
- Tag-first matching: a file's own `MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID`
  tags resolve it exactly; otherwise whole-folder release matching, then
  fuzzy title search, fill the gap. Matched files can be renamed into a
  configurable layout and have their tags rewritten.
- Poster-grid browsing with artist detail pages (owned albums grouped by
  type, a Missing section, a Wanted section), health checks, backups, and a
  log viewer.
- Optional login with **admin/member roles**: members get everyday use,
  admins get the server's configuration and accounts.
- Artist bio/photo and album art are cached automatically the moment an
  artist is discovered (monitored, or found by a scan) — never fetched
  again just from browsing.
- Local image cache: provider art is downloaded on add/refresh and served
  from CantiNode, surviving provider link rot.

Start with [Installation](installation.md), then the
[Quickstart](quickstart.md).

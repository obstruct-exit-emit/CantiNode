# CantiNode

A self-hosted **music library** automation server — an alternative to
Lidarr.

CantiNode monitors artists you want, searches your indexers, and hands
picked releases to your download client; scanning matches files against
MusicBrainz and can organize them for you.

> 🚧 CantiNode is **pre-1.0**. The whole loop works end-to-end, but expect
> rough edges and breaking changes until 1.0.

## The library

Music is the only library. It's Plex-style: it appears in the UI once you
add a root folder for it.

| Type | Metadata | Formats |
|---|---|---|
| Music | MusicBrainz (artist/album/track identity) + TheAudioDB (bio/photo) | flac, mp3, m4a, opus, wav |

## Highlights

- One acquisition pipeline: Newznab/Torznab indexers, or a single Prowlarr
  connection that searches everything Prowlarr already has configured,
  release parsing and scoring, quality profiles with upgrades, qBittorrent
  and SABnzbd.
- A built-in **direct** HTTP fetcher for plain-HTTP sources, alongside
  Newznab/Torznab. See [Acquisition](acquisition.md).
- **Automatic acquisition end to end**: a background sweep searches and
  grabs monitored artists' wanted albums on its own (default daily, tunable),
  and a finished download is copied into the library and scanned in
  automatically — no manual "Search releases" or "Scan files" step needed
  unless you want one (e.g. to search an unmonitored artist, or grab a
  quality upgrade for something you already own).
- Tag-first matching: a file's own `MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID`
  tags resolve it exactly; otherwise whole-folder release matching (CD1/CD2/
  Disc-N subfolders of one album are detected and merged first), then
  fuzzy title search, fill the gap — with a specific release version/edition
  pickable instead of one fixed default. Matched files can be renamed into a
  configurable layout and have their tags rewritten. Anything left unmatched
  gets a dedicated review page: manual per-file search, or a confidence-gated
  auto-match against your own library.
- Poster-grid browsing with artist detail pages — one **Albums** grid holding
  both owned and wanted albums (badged accordingly, with an inline release
  browser to search/grab a wanted one) and a **Missing** section grouped by
  release type for the rest of the discography — health checks, backups,
  and a log viewer.
- Optional login with **admin/member roles**: members get everyday use,
  admins get the server's configuration and accounts.
- Artist bio/photo, genres/tags/rating, and album art (including for
  wanted/missing albums, not just owned ones) are cached automatically the
  moment an artist is discovered (monitored, or found by a scan) — never
  fetched again just from browsing.
- Local image cache: provider art is downloaded on add/refresh and served
  from CantiNode, surviving provider link rot.

Start with [Installation](installation.md), then the
[Quickstart](quickstart.md).

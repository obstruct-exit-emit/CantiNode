# LibriNode

A self-hosted media automation server for **written media** — an
alternative to tools like Readarr and LazyLibrarian that manages ebooks,
audiobooks, manga, comics, and magazines in one app.

LibriNode monitors your wanted list, searches your indexers, sends releases
to your download client, then imports, renames, and organizes files into
per-type libraries — automatically.

> 🚧 LibriNode is **pre-1.0**. The whole loop works end-to-end, but expect
> rough edges and breaking changes until 1.0.

## The five libraries

| Type | Metadata | Formats |
|---|---|---|
| Ebooks | Hardcover (+ Open Library / Google Books fallbacks) | epub, mobi, azw3, pdf |
| Audiobooks | Hardcover (+ Open Library / Google Books fallbacks) | m4b, m4a, mp3, flac, opus |
| Manga | AniList (no key) or Hardcover | cbz, cbr, epub |
| Comics | Hardcover or ComicVine (free key) | cbz, cbr, pdf |
| Magazines | none — added by name (organize-only for now) | pdf, epub, cbz |

Each library is independent: its own root folders, naming templates, and
quality profiles. Plex-style, a library only appears in the UI once you
create it by adding its root folder. For prose books, Ebooks and Audiobooks
are **separate libraries** — a book belongs only to the format libraries you
added it to (or own).

## Highlights

- One acquisition pipeline for everything: Newznab/Torznab indexers (or
  Prowlarr sync), release parsing and scoring, quality profiles with
  upgrades, qBittorrent and SABnzbd, Completed Download Handling.
- **Native indexers** for sources Prowlarr can't reach (AudioBook Bay,
  Library Genesis) plus a built-in **direct** HTTP fetcher — all off by
  default, user-enabled, hidden from Prowlarr. See
  [Acquisition](acquisition.md).
- Reader-friendly layouts: Audiobookshelf folders for audio, Kavita/Komga
  layouts with `ComicInfo.xml` for comics, OPF sidecars for Calibre.
- Poster-grid browsing with detail pages, per-library Wanted pages, a
  release calendar, health checks, backups, and a log viewer.
- Optional login with **admin/member roles**: members get everyday use,
  admins get the server's configuration and accounts.
- Manga/comic extras: per-series Missing view with selective monitoring
  (adds pull metadata only), colorized/monochrome manga variants in one
  library, and covers from the provider or extracted from the owned CBZ/CBR.
- Local image cache: provider art is downloaded on add/refresh and served
  from LibriNode, surviving provider link rot.

Start with [Installation](installation.md), then the
[Quickstart](quickstart.md).

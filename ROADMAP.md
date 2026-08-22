# 🎵 CantiNode Roadmap

Where the project has been and where it's going. Phases 0–5 are **complete**;
Phase 6 (hardening) is nearly done, with the remaining work gated on things
that take calendar time or external resources rather than code. The
fine-grained record of every change lives in the [CHANGELOG](CHANGELOG.md).

**Legend:** ✅ complete · 🔄 in progress · ⏳ externally gated · 💡 under consideration

> **Lineage:** CantiNode began as its own from-scratch music organizer, was
> then rebuilt on top of a fork of [LibriNode](https://github.com/obstruct-exit-emit/LibriNode)
> (a books/*arr server, hence phases 0–2/4–6 below reading like a book app's
> history — they describe that shared codebase before it narrowed back to
> music), and has since had every non-music feature removed. See the
> [CHANGELOG](CHANGELOG.md#removed).
>
> **Since removed:** manga and magazines (media types, Phase 3) and the
> AudioBook Bay / Library Genesis native indexers (Phase 5) were pulled from
> the codebase entirely, and later **ebooks, audiobooks, and comics were
> removed outright** — CantiNode is now a **music-only** server. The phase
> write-ups below are left as delivered-at-the-time history.
>
> **Since changed:** Phase 2's "Prowlarr application sync" (add CantiNode to
> Prowlarr as a fake Readarr app, which pushes its indexers into CantiNode)
> was replaced with a direct connection — CantiNode searches Prowlarr's own
> `GET /api/v1/search` instead. See the [CHANGELOG](CHANGELOG.md#changed).

## At a glance

| Phase | Scope | Status |
|---|---|---|
| [0 — Foundation](#phase-0--foundation-) | Stack, schema, config, CI | ✅ |
| [1 — Library core](#phase-1--library-core-) | Metadata, browsing, scanning, organizing | ✅ |
| [2 — Acquisition](#phase-2--acquisition-) | Indexers, scoring, download clients, auto-search | ✅ |
| [3 — Five media types](#phase-3--five-media-types-) | Audiobooks, manga, comics, magazines | ✅ |
| [4 — Experience & administration](#phase-4--experience--administration-) | Plex-style UI, auth & roles, health, backups, themes | ✅ |
| [5 — Reach](#phase-5--reach-) | Native indexers, direct downloads, metadata fallbacks | ✅ |
| [6 — Hardening & release](#phase-6--hardening--release-) | Proving it, packaging it, shipping it | 🔄 |
| [Next steps](#next-steps-) | Concrete, prioritized work from here | 🎯 |
| [Future](#future-) | Ideas under consideration | 💡 |

---

## Phase 0 — Foundation ✅

- Go backend + React frontend, compiled into **one self-contained binary** per OS
- SQLite (pure Go, no cgo) with an embedded, tested migrations framework
- Config file + `CANTINODE_*` env overrides, rotating logs, cross-platform data dirs
- Versioned REST API (`/api/v1`) with API-key auth — the same API the UI uses
- CI building and testing on Windows and Linux; GPL-3.0

## Phase 1 — Library core ✅

- Author / Series / Book / Edition data model with **explicit per-format
  library membership** — a book belongs to Ebooks and/or Audiobooks only where
  you added or own that format, never by inference
- **Hardcover** metadata provider (live-verified): search, lookups, covers, editions
- Provider registry with hot-swap — token changes apply without a restart
- Library scanning that matches files in layers: **ISBN/ASIN identifiers**
  (from filenames or embedded epub metadata, checksum-validated) → exact
  author/title → **fuzzy suggestions**, offered for one-click confirmation but
  never auto-imported
- Existing-file import with 0–100% confidence ratings, duplicate resolution
  (replace/delete), and one-click adds for unknown authors/series/magazines
- Naming templates for every media type with live preview and
  **preview-then-apply** organize — library-, author-, series-, and book-scoped
- Scheduled + manual metadata refresh (per-record, per-library, global),
  honoring per-record provider overrides
- Local image cache: provider art served by CantiNode, surviving link rot

## Phase 2 — Acquisition ✅

- **Newznab/Torznab** indexer framework: per-type categories, Test buttons,
  per-indexer exponential failure backoff
- **Prowlarr application sync** (live-verified) — add CantiNode as a *Readarr*
  application and Prowlarr pushes both usenet and torrent indexers automatically
- Release parsing + scoring: formats, retail, language, year, narrators,
  bitrate, volume ranges, issue dates — book-aware search rejects wrong matches
  and ranks the rest
- Quality profiles per media type: ordered formats, language, size bounds, and
  **upgrade handling** with cutoffs; upgrades replace the old file
- **qBittorrent** and **SABnzbd** clients (live-verified through a debrid
  bridge): CantiNode resolves releases on its own side (magnet / `.torrent` /
  NZB upload), so NAT'd clients and Real-Debrid/TorBox bridges just work
- Completed Download Handling: automatic import, rename, clean-up, seed-goal
  awareness, and a failed-release blocklist with instant replacement search
- **Multi-book pack imports**: a "complete series" grab fills every matching
  book — matched by volume/title, never by size
- **Remote path mappings**: clients on other machines or in containers map
  their reported paths to local ones — longest prefix wins
- Automatic wanted-list sweeps (tunable cadence) + interactive per-book search

## Phase 3 — Five media types ✅

**Audiobooks** — a separate library from ebooks; audio-aware parsing
(narrator, bitrate, abridged rejection); multi-file books scanned, imported,
and organized as single units; Audiobookshelf-ready layouts with
`metadata.opf` sidecars.

**Manga & comics** — series-first libraries: **AniList** (keyless) or
**Hardcover** for manga, **Hardcover** or **ComicVine** for comics, switchable
with in-place re-sourcing; per-series Missing sections with selective or bulk
monitoring ("monitor future volumes" included); whole-series **pack search**
that ranks complete ranges above partial ones; `ComicInfo.xml` written into
imported CBZs; Kavita/Komga-ready layouts; covers from the provider or
extracted from the owned archive's first page; manga **colorized/monochrome
variants** owned side by side in one library.

**Magazines** — provider-less periodicals added by name; issues recognized by
date or number in filenames; scanning materializes owned issues; per-year
folder layouts. *Organize-only for now* — acquisition is disabled (the
magazine usenet landscape proved to be mostly disguised malware); the search
engine stays in the tree for when it returns.

## Phase 4 — Experience & administration ✅

- **Plex-style navigation**: a library appears only once you create it; poster
  grids with owned/total counts; author → book pages for prose, series pages
  for the rest; Home shows per-library Recently-added and Wanted rows
- Per-author and per-series **Missing** sections with one-click and bulk monitoring
- Per-library **Wanted** cards, a cross-library release **Calendar**, and a
  live **Activity** queue with grab history
- **Per-file actions** on detail pages: organize or delete a single file in place
- **Multi-user auth with roles**: admins run the server, members use it —
  enforced by the backend, not just hidden in the UI; sessions bound to their
  accounts; a first-run setup wizard; a visual folder browser
- **Light and dark themes** with an Auto mode that follows the OS live —
  per-browser preference, applied before first paint
- Grouped settings with Test buttons on every connection and advanced options
  tucked behind toggles
- Health checks every 15 minutes with self-explaining banners that distinguish
  "unreachable" from "rejected credentials" and recover on their own
- Backups (consistent DB snapshot + config) with **staged restore**; a log
  viewer with rotation; delete-from-disk options that never escape a root folder
- Packaging: Dockerfile + compose, systemd unit, Windows scripts, release CI

## Phase 5 — Reach ✅

Extending past what the standard *arr APIs can see:

- **Native indexer framework**: built-in Go sources selected as an indexer
  *type* — no Newznab endpoint — feeding the same search/scoring/grab
  pipeline, and hidden from Prowlarr so sync never collides. **Off by
  default, user-enabled, user-responsible** (the same dual-use posture as
  Prowlarr's own definitions)
- **AudioBook Bay** (audiobooks): scrapes listings and assembles the magnet
  from the on-page info hash + trackers; rides the normal torrent path;
  primary + fallback site URLs for its rotating domains
- **Library Genesis** (ebooks): searches libgen.li and downloads by MD5 via
  its `ads.php` → `get.php` mirror, no account needed. Each result carries its
  author, year, language, and format, so search keeps only the book you asked
  for in the language and format your quality profile wants. (Anna's Archive
  was tried and dropped: it renders search behind a JS/anti-bot wall that needs
  a browser/bypasser CantiNode doesn't ship — and Libgen is what it aggregates.)
- **`direct` download protocol**: CantiNode's own HTTP fetcher — mirror-list
  failover, landing-page awareness (follows `ads.php` → `get.php` to the file),
  progress in the queue, imported like any other grab; any direct-link source
  can ride it
- **Metadata fallbacks**: Open Library and Google Books (both keyless) answer
  only when the primary draws a blank; records remember their source so
  refreshes route back to it
- Credential hygiene throughout: keys and tokens are scrubbed from every
  error, banner, and log line

## Phase 6 — Hardening & release 🔄

Turning "works on the dev box" into "trustable release". Done so far:

- ✅ Live end-to-end verification: Prowlarr sync, torrents and NZBs through a
  TorBox/Real-Debrid bridge — search → grab → download → import → organized file
- ✅ Automated **migration testing** (old-schema fixtures driven through the
  full chain — migration bugs are data-loss bugs) and an automated
  **clean-machine restore drill**
- ✅ Failure-mode polish: provider outages, stuck indexers, and vanishing
  download clients all degrade into readable banners and recover on their own
- ✅ Security pass: session binding, constant-time key checks, path-traversal
  audit, and a token-leak sweep with regression tests
- ✅ Performance pass at ~11,000-book scale (batched scan transactions cut a
  cold scan from 32s to 2.5s; oversized payloads trimmed)
- ✅ Release hygiene: version-stamped builds, a CHANGELOG, and a release CI
  (tag `v*` → GitHub release) proven out on the codebase this was forked
  from — not yet exercised with a tag on this repo, which has none yet

Docker and Windows support (both shipped earlier in this phase — a
Dockerfile + compose file + published GHCR images, and a Windows
zip + Task Scheduler install script) are **on hold for now**, pulled back to
concentrate on a single well-supported Linux path through burn-in; both
return post-1.0 (see [Future](#future-)).

Remaining — externally gated:

- [ ] ⏳ **Real-world burn-in**: weeks of daily use with real libraries, messy
  release names, and provider rate limits
- [ ] ⏳ **Docs stranger-test**: a fresh person follows the quickstart from
  scratch (the code-audit pass is done; the human walkthrough remains)

**1.0 ships when burn-in comes back clean.**

## Next steps 🎯

Picked 2026-08-11, after a session of live bug-fixing (release scoring,
delete-files, Activity page lag) — concrete and prioritized, unlike
[Future](#future-) below:

1. [x] **Automatic wanted-list sweep** — done overnight 2026-08-11:
   `internal/autosearch` sweeps every monitored artist's still-wanted
   albums on a timer (default 24h, tunable under Settings → Background
   timings), auto-grabbing the best approved release exactly like a manual
   "Search releases" click. Unmonitored artists' wanted albums are left
   alone, same as before. Verified live — the first sweep after deploy
   found and grabbed a real release within seconds of startup.
2. [x] **Wire up "Upgrades allowed"** — done overnight 2026-08-11: new
   `GET/POST /api/v1/music/album/{id}/upgrade/search|grab` endpoints let
   the album page search for (and grab) a better release than what's
   currently owned, scored with `release.Preferences.MinFormatScore` so
   only a genuine step up ever approves. Deliberately **manual-trigger
   only**, not folded into the automatic sweep above — an unattended loop
   silently grabbing a second copy of an already-owned album felt like a
   judgment call that deserved a human in the loop. The grabbed file also
   isn't auto-swapped in for the old one yet (it lands alongside it;
   removing the old file is still a manual step) — a reasonable follow-up
   if this gets used a lot.
3. [ ] **Real-world burn-in** — the Phase 6 gate above: run it for real,
   watch `journalctl -u cantinode` and Settings → Health for anything that
   surfaces on its own. One unsupervised night in (2026-08-11, alongside
   items 1/2/5 going live) came back clean, but that's a start, not the
   "weeks of daily use" this gate actually wants.
4. [x] **Upgrade "Add artist" search results** — done 2026-08-13:
   `AddArtistPanel` now renders the same `.poster-grid`/`.poster-card`
   pattern the Library grid and an artist's Albums grid already use,
   instead of a bare name-plus-button text list. MusicBrainz's artist
   search returns no image at all, so every card uses the same lettered
   fallback tile `WantedPoster` degrades to when there's genuinely no cover
   art — but `musicbrainz.Artist` and the search response now also carry
   `type`/`country`/`disambiguation` (previously fetched but never
   surfaced), shown as the card's subtitle, which is the one thing that
   actually helps pick the right artist among several same-named results.
5. [x] **A deliberate pass over delete/scan/organize edge cases** — done
   overnight 2026-08-11, two real bugs found and fixed the same way the
   delete-files regression and Activity lag were: removing an artist/album
   with a grab still in flight now cancels it first (it used to finish and
   silently re-import, resurrecting what was just removed); the album
   page's "Scan files" now notices and prunes a file — or its whole
   folder — deleted outside the app, which it previously left as a stale,
   undetected row forever.
6. [x] **Release-version selection, fuller metadata caching, and
   multi-disc-aware auto-matching** — done overnight 2026-08-11: a specific
   MusicBrainz release/edition can now be matched against directly instead
   of one fixed default, every version's metadata and tracklist is cached
   (with a backfill sweep for artists that predate it), artist genres/tags/
   rating are cached alongside bio/photo, artist removal now actually
   purges its cached metadata (previously orphaned forever), wanted/missing
   albums show cover art too, CD1/CD2/Disc-N folders are detected and
   merged for matching, and the unmatched-files page's auto-match gets a
   confidence-gated pre-fill for artist/album/version. Two real bugs found
   against the live library after deploy and fixed same-night: a migrated
   placeholder row was mistaken for "fully cached" (blocking the backfill
   sweep and the version dropdown for every pre-existing artist), and
   per-disc Album-tag suffixes ("Album CD 1"/"Album CD 2") sank the
   auto-match album-name score below its confidence threshold. See
   [CHANGELOG](CHANGELOG.md).
7. [x] **Auto-swap the old file after an "Upgrades allowed" grab** — done
   2026-08-13, delete-outright (the judgment call this item flagged):
   `grabs.upgrade_album_id` (migration 024) ties an upgrade grab to the
   album it's for, the way `wanted_album_id` already ties a normal grab to
   a wanted album. Once `internal/importer` scans a completed upgrade
   grab's files in, `swapUpgradedFiles` compares the album's track files
   before and after the scan and deletes the old file for every track that
   just gained a genuinely new matched one — track-by-track, not the whole
   album at once, so a partial/failed match on the new release can never
   leave a track with nothing.
8. [x] **Sort the Library grid** — done 2026-08-13: `SortControl.tsx` gets
   a `sortArtists` (name / recently-added / album count / missing count,
   mirroring `sortAlbums`/`sortReleaseGroups`), wired into
   `MusicLibraryView.tsx` via the same `SortSelect`/`DirectionButtons` an
   artist's own Albums grid already uses.
9. [x] **Retry a failed grab from Activity** — done 2026-08-13: a failed
   grab's history row now shows "Search again" (whenever it's tied to a
   wanted album or an upgrade, which is every music grab) — expands the
   same `ReleaseBrowser` the album page itself uses, right there in
   Activity, instead of sending the user off to find the album by hand.
10. [x] **Composer credit in "Write tags"** — done 2026-08-22: resolved
    from MusicBrainz's own work-relationship data (a recording's linked
    Work carries composer/writer credit, not the recording itself), via
    direct recording/release lookups. Deliberately left blank for a file
    matched through the batched recording-search fast path — MusicBrainz's
    search endpoint never returns relationship data regardless of `inc`
    params (confirmed live), so paying for a second per-track lookup just
    for composer would defeat the point of batching. Once resolved it's
    only ever upgraded on a re-match, never blanked, so a track that
    already has a real credit keeps it even if later re-matched via a path
    that can't supply one.
11. [x] **Per-field "Tags to Write" toggle** — done 2026-08-22: Settings →
    Music → "Tags to Write" lets every field "Write tags" can embed
    (title, artist, genre, composer, cover art, MusicBrainz IDs, ...) be
    switched off individually, all on by default. A disabled field is
    left completely untouched by a write — never set, never cleared —
    the same treatment a field CantiNode simply has no data for cached
    yet already gets.
12. [x] **"Retry cover art" and a real fix for the bug it works around** —
    done 2026-08-22: found live that two Blind Melon albums stayed
    permanently cover-less because a stale Cover Art Archive "no cover"
    result (cached before TheAudioDB support ever existed) blocked
    `GetFrontCover` from trying TheAudioDB at all, even once it genuinely
    had the art. Both sources now get their own independent negative
    cache. A small retry control also sits right over an album's missing
    cover (the album page and every artist page's albums grid) for a
    manual re-check on demand, rather than waiting out either source's
    own ~30-day stale-miss window.
13. [x] **MusicBrainz self-hosted mirror setting** — done 2026-08-22:
    Settings → Music → MusicBrainz gets a "Server URL" field, blank by
    default (the real musicbrainz.org). For an operator who runs their
    own MusicBrainz-API-compatible mirror — not a way to borrow anyone
    else's infrastructure; CantiNode ships with no bundled or suggested
    mirror to point this at.
14. [x] **Background-operation audit** — done 2026-08-22: a deliberate
    pass over every action that can outlive the request that started it
    (write tags, organize, move to a different root folder, a full scan,
    grabbing a release, adding an artist). Found and fixed the one real
    gap: grabbing a release still tied its download-client submission to
    the browser's own request context, so a page refresh mid-grab could
    abandon an add that would otherwise have landed — the same class of
    bug an earlier fix (decoupling match-approval from the browser's
    request context) had already addressed elsewhere. Also found and
    fixed that the MusicBrainz client's retry logic only ever covered a
    bad HTTP status code, never a transport-level failure (a timeout, a
    connection reset) — invisible for an ordinary artist, but a real
    problem for an outlier-prolific one (Elvis Presley's 1,342 release
    groups need ~14 sequential paginated requests, raising the odds any
    single one hits a transient blip) that used to fail the whole
    operation instead of just that one page.
15. [x] **Folder/filename fallback for auto-match** — done 2026-08-22: a
    file with blank Artist/Album tags used to be left unmatched no matter
    how well-organized its folder structure was. Now falls back to the
    filename (only when it has 3+ `" - "`-separated segments — a plain
    two-segment name is too ambiguous to guess which part is which), then
    the containing folders (album folder, then artist folder one level
    up, bounded by the file's own root folder so a flat layout never
    mistakes the root folder's own name for an artist). Matching input
    only — a file's own cached/displayed tags are never touched by it.
    Reaches both the per-file fuzzy path and the whole-folder consensus
    path, so a whole album of completely untagged files can now reach a
    match from its folder structure alone.
16. [ ] **Import Lists** — scoped 2026-08-22 (a Lidarr feature CantiNode
    was missing — see [CHANGELOG](CHANGELOG.md) for the comparison that
    prompted this), not yet built. Point CantiNode at an external source;
    on a periodic sweep it resolves the source to MusicBrainz artist
    MBIDs and adds+monitors any new artist automatically (matching
    Lidarr's own default), so it joins the existing autosearch sweep with
    no manual step. Add-only — an artist that later falls off a list
    stays in the library, matching CantiNode's existing "never
    auto-delete" posture elsewhere.
    - **v1 source types, decided**: MusicBrainz Series (no new
      integration — reuses `musicbrainz.Client.LookupSeries`, which
      already exists and already dedupes/sorts its linked release
      groups), a plain list (pasted text or a fetched URL, one artist
      name per line, resolved the same way a manual "add artist" search
      does), and Last.fm (a user's top artists, or a tag/genre's top
      artists — needs a new Last.fm API key setting mirroring the
      existing TheAudioDB key). **Deliberately deferred**: Spotify
      (OAuth login-flow complexity is its own separate piece of work)
      and MusicBrainz Collections (not even supported by Lidarr itself
      yet — still an open feature request there).
    - **Design, grounded in a research pass over CantiNode's own existing
      conventions** (indexer/download-client CRUD, the
      autosearch/discoveryrefresh/metadatabackfill periodic-service
      shape, `TimingSettings`' tunable-interval pattern, and the
      Settings-page card conventions), so this is ready to build, not
      just an idea:
      - New `internal/importlist` package: a `discoveryrefresh`-shaped
        `Service`/`RunPeriodic`/`PollOnce` (simple periodic, no
        daily-schedule mode needed).
      - New `import_lists` table (next migration): flat typed columns —
        id, name, type, per-type config fields, enabled, added_at,
        last_synced_at, last_sync_error — matching the
        `indexers`/`download_clients` convention rather than a JSON blob.
      - Per-type resolver: MusicBrainz Series via the existing
        `LookupSeries`; Last.fm via a new keyless-per-user
        `user.getTopArtists`/`tag.getTopArtists` call resolved to MBIDs
        via the existing `SearchArtists`; plain list via the same
        search-based resolution over a line-per-artist source.
      - Each resolved MBID goes through the same cheap path
        `handleQuickAddMusicArtist` already uses (`LookupArtist` →
        `GetOrCreateArtist` → `SetArtistMonitored(true)` →
        `discography.Service.RefreshArtist`), leaving full bio/version
        backfill to the existing `metadatabackfill` periodic sweep
        rather than paying for it synchronously per artist during an
        unattended sweep of potentially many artists at once.
      - REST CRUD mirrors `internal/api/indexers.go` exactly: `GET/POST
        /api/v1/importlist`, `PUT/DELETE /api/v1/importlist/{id}`,
        `POST /api/v1/importlist/test` (validate an unsaved draft
        resolves without erroring, same as the indexer Test button).
      - Sync cadence is a new `TimingSettings.ImportListSyncIntervalMinutes`
        (24h default, matching Lidarr's own default) — surfaces through
        the existing `GET/PUT /api/v1/settings/timings` route and
        `TimingsPanel`, no new settings route needed for cadence.
      - UI: a new `ImportListsCard` in `web/src/views/SettingsView.tsx`,
        structurally identical to the existing `IndexersCard`/
        `DownloadClientsCard` (saved-list with test/enable-toggle/edit/
        remove row actions; one add/edit form below with a type dropdown
        driving conditional per-type fields).

## Future 💡

Under consideration, in no particular order:

- [ ] **Spotify import lists** — deferred out of the initial [Import
  Lists](#next-steps-) work (item 16 above): followed artists, saved
  albums, and playlists as a fourth import-list source type, pending an
  OAuth login-flow design (the other three v1 source types are all
  keyless or bring-your-own-API-key, no user login flow needed).
- [ ] **Docker and Windows support, returning**: Dockerfile + compose,
  published GHCR images, and a Windows zip + install script all worked and
  shipped before being pulled back mid-Phase-6 to concentrate on one
  well-supported Linux path through burn-in — reintroducing them, plus the
  originally-planned code-signed Windows installer, is a matter of picking
  the work back up, not redesigning it
- [ ] **External notifications**: Discord/webhook/email on grab, import,
  upgrade, and failure
- [ ] **Player integrations**: notify Navidrome/Plex on import
- [ ] **Accessibility, the systematic pass**: focus trapping, full keyboard
  paths, a screen-reader walk of the main flows
- [ ] **Localization** — and with it, language/date preferences

---

*History: the [CHANGELOG](CHANGELOG.md) records every feature and fix in
detail; [docs/](docs/index.md) documents how everything behaves today.*

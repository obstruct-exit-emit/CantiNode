# Changelog

Notable changes to CantiNode. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/); versions follow semver.
No tags have been cut on this repo yet — `v0.9.0` is planned as the first,
once the pre-1.0 hardening (see [ROADMAP](ROADMAP.md)) wraps up.

## [Unreleased]

Everything to date — Phases 0–5 (feature-complete) plus the pre-1.0 hardening
in progress. Highlights from the hardening period, newest first:

### Added
- **The artist page also gets MusicBrainz/TheAudioDB link icons, the
  same pair the album page has.** MusicBrainz is a plain client-side link
  (`musicbrainz.org/artist/{mbid}`, or `/series/{mbid}` for a tracked
  series — a series MBID is a real, distinct MusicBrainz entity type with
  its own browsable page). TheAudioDB needed the same kind of small
  backend addition as the album version: `handleAudioDBArtistLink`
  mirrors `handleAudioDBAlbumLink` exactly (a live, on-click-only lookup,
  nothing stored), and is hidden entirely for a series — TheAudioDB has
  no concept of one, only real artists. `audiodb.ArtistMeta` widened to
  carry `IDArtist` alongside its existing Bio/ImageURL.
- **The album page's stat row now shows which specific edition/pressing
  is owned — e.g. "2×CD · Limited Edition Artbook".** Sourced entirely
  from `release_group_versions`, already cached by the existing
  discography-version sync (media summary + disambiguation for every
  known release of a release group); the frontend just finds the one
  entry matching the album's own release MBID via the versions endpoint
  that already existed for the matching UI's version picker. No backend
  changes, no new network cost — a plain local DB read either way.
- **The album page now shows a TheAudioDB description, cached the same
  way as every other piece of TheAudioDB/MusicBrainz metadata.** New
  `albums.description`/`description_fetched_at` columns (migration 029)
  mirror `artists.bio`/`metadata_fetched_at`'s own convention exactly:
  fetched once, on the album's first-ever page view, then never
  re-queried — `description_fetched_at` (not description's own emptiness)
  is what distinguishes "never tried" from "tried, TheAudioDB had
  nothing." A later view of the same album gets the cached description
  for free from the plain album fetch, no extra request at all; only a
  genuinely first-ever view pays for the one-time lookup, via a new
  `GET .../album/{id}/description` endpoint the frontend calls
  separately so it never blocks the rest of the page rendering.
- **The album page shows one shared base folder path instead of every
  track's own full path, plus MusicBrainz/TheAudioDB links.** Each
  track's file row used to repeat its complete absolute path — noisy for
  an already-organized library where every path is predictable; now the
  album header shows the one folder every track's file lives under (the
  deepest shared directory, computed client-side — correctly collapses a
  multi-disc album's CD1/CD2 subfolders to their shared parent), and each
  track row is just format/size (full path still available as a hover
  tooltip). Also added "MusicBrainz ↗"/"TheAudioDB ↗" links next to the
  track count: MusicBrainz's is a plain client-side link (its URLs are
  MBID-based, already in hand); TheAudioDB's is a new backend redirect
  endpoint (`GET .../album/{id}/audiodb-link`) since TheAudioDB's own
  site URLs use its internal numeric album id, not the MBID — looked up
  live only the moment someone actually clicks the link, not stored or
  fetched on every page load.
- **The album page's header got a labeled stat row (Path/Quality/Size),
  loosely inspired by the *arr family's own movie/show detail pages —
  not copied 1:1, kept in CantiNode's own dark/purple visual language.**
  The bare folder emoji next to the path is gone; Path now sits in its
  own small-label-over-value stat alongside two new ones: Quality
  (Lossless/Lossy/Mixed, classified from the owned files' own formats —
  container-level, not true codec sniffing, so an M4A/OGG file is
  assumed to hold the common lossy codec rather than the rarer lossless
  one the same container can technically carry) and Size (total bytes
  owned). The MusicBrainz/TheAudioDB link row also got its own margin so
  it no longer sits flush against the action buttons below it.
- **"Write tags" is now a bulk, album- and artist-scoped action, matching
  Organize.** The album/artist pages' per-file organize/write-tags/delete
  buttons cluttered every track row for something that's almost always
  done for a whole album or artist at once. Organize already had
  album/artist-scoped versions; write-tags didn't, so it stayed
  per-file — new `WriteTagsForAlbum`/`WriteTagsForArtist` (best-effort per
  file, an unmatched file silently skipped, one failure doesn't stop the
  rest — the same pattern `OrganizeArtist`/`OrganizeAlbum` already use) back
  new "Write tags" buttons next to each page's own "Organize…". The
  per-file organize/write-tags/delete buttons are gone from the album
  page's track rows entirely — each file row is now just path/format/size.
- **Batched MusicBrainz recording lookups for direct-matched folders.**
  `matchFolder` used to resolve every file's embedded
  `MUSICBRAINZ_TRACKID` with its own `LookupRecording` round trip — a
  folder of 12 tagged files cost 12 separate requests at MusicBrainz's
  ~1.1s throttle before whole-folder matching even started. A new
  `musicbrainz.Client.BatchLookupRecordings` resolves a whole folder's
  worth of recording IDs in one request via MusicBrainz's own
  `rid:(id1 OR id2 OR ...)` search syntax (chunked at 50 IDs/request);
  `matchFolder` now batches every direct-MBID file in a folder through
  it, applying the same tag/title-consistency checks
  (`resolveDirectMatch`, extracted out of `matchFileDirect`) to each
  result. Falls back to today's per-file lookups if the batch call
  itself fails outright — never worse than before.
- **The new-artist metadata backfill sweep is now its own independent
  periodic background loop, restart-safe against interruption.**
  Previously, filling in discography/bio/photo for an artist a scan
  discovered implicitly only ever ran once, synchronously, right after
  that scan — tied to the scan's own goroutine, so a process restart
  mid-sweep (a redeploy, a crash) killed it outright with nothing to
  resume it until the next full scan. New package
  `internal/metadatabackfill` (mirroring `internal/discoveryrefresh`'s
  own periodic-sweep shape) now also runs this on its own 15-minute
  timer, independent of any particular scan — an interrupted pass is
  always picked up again within one interval. Also now the shared home
  for the discography/version/tracklist/bio caching logic previously
  spread across several `internal/api` methods.
- **A title sanity check alongside the release-group one, and declined
  direct matches now auto-resolve via folder consensus instead of just
  sitting in Unmatched.** Two follow-ups to the tag-consistency fix
  above. `matchFileDirect` now also compares the looked-up recording's
  own title against the file's `TITLE` tag (`titleAgrees`, reusing the
  existing `titleSimilarity` helper — no new dependency) — catches a
  stale embedded recording ID that's simply the wrong song, even with no
  release-group tag to contradict it. And a file declined by either
  check no longer stops there: `matchFolder` catches the decline via a
  new sentinel (`errDirectMatchInconsistent`) and gives the file a real
  shot at whole-folder consensus matching, using its still-reliable
  `MusicBrainzAlbumID`/`Album` tags instead of the mismatched recording
  one — closing the loop so a case like the Birdy one auto-resolves
  correctly instead of just being correctly flagged for manual review. A
  genuine lookup failure (network, 404) is unaffected and still surfaces
  in scan errors exactly as before — only a positively-detected internal
  tag inconsistency reroutes.
- **A new periodic discography refresh, and a real fix for a class of
  wrong-album matches — two robustness improvements aimed at closing the
  gap with Lidarr's own polish, prompted by a real live bug (a Birdy
  compilation track landing on her own studio album instead).** (1)
  `internal/discoveryrefresh` re-checks every monitored artist's/tracked
  series' own discography against MusicBrainz on a schedule (default
  24h, matching Lidarr's own default "Refresh Artist" cadence, tunable
  under Settings → General → Advanced: background timings) — a new
  release now lands in Missing on its own instead of only ever showing
  up after a manual "Refresh metadata" click. Deliberately kept to just
  the cheap discography-only check (one MusicBrainz request per artist
  in the common case, shared with the manual-refresh path via the new
  `internal/discography` package) — bio/photo and per-release-group
  version caching stay on their existing paths so this is safe to run
  unconditionally across a whole library. Never auto-wants a newly-found
  release — lands in Missing only, same as monitoring already works. (2)
  `matchFileDirect` (the embedded-MusicBrainz-ID fast path) now cross-
  checks a file's `MusicBrainzReleaseGroupID` tag against its
  `MusicBrainzRecordingID` tag's own known releases before trusting the
  recording ID absolutely. Traced live: a compilation track's recording
  ID can point at a MusicBrainz recording entry that's only linked to an
  artist's own single/album, disconnected from the actual compilation
  release the rest of the file's tags correctly name (a real MusicBrainz
  data-duplication pattern, not a CantiNode bug) — previously matched
  confidently to the wrong album; now declines the fast path and leaves
  the file for manual/auto-match review instead.
- **A second way to add music: paste a MusicBrainz Series link.** A
  numbered compilation series (e.g. "Now That's What I Call Music!") can
  now be added and monitored as its own artist page — every entry becomes
  an album under it, instead of scattering across whichever real
  MusicBrainz artist-credit each one carries (almost always "Various
  Artists" for a compilation series). Behaves like monitoring a real
  artist otherwise: discography lands in Missing, `Refresh metadata`
  re-syncs it (no bio/photo — a series has neither), no background
  auto-resync or auto-wanting of new entries. `artists` gained a `kind`
  column (migration 028) distinguishing a series-backed row from a real
  MusicBrainz artist; matching (`applyMatch`) now checks whether a
  release group is tracked under a series before falling back to the
  recording/release's own real artist-credit, so a grabbed/matched entry
  actually lands owned under the series instead of a stray new "Various
  Artists" row with its wanted counterpart stuck forever. Search scoring's
  relevance gate (`artistRelevant` in `internal/release` — rejects a
  candidate release whose title doesn't plausibly name the wanted artist)
  also had to learn to skip itself for a series artist, since a series'
  own name essentially never
  appears verbatim in a release's file/torrent name the way a real
  artist's does.
- **Series-add now also supports "Release series"**, not just "Release
  group series" — found live minutes after shipping the series-add feature
  above, pasting a real "Release series" link ("Cities 97.1 Sampler")
  rejected outright. The two are different MusicBrainz series kinds: a
  "Release group series" links release groups (albums) directly, but a
  "Release series" links specific releases (editions/pressings) instead,
  and MusicBrainz doesn't include a release's own release group in that
  relationship data at all (`release-groups` isn't even a valid `inc` for
  the series resource). `LookupSeries` now makes one further, best-effort
  lookup per release entry to resolve its release group (a real,
  sequential, rate-limited cost — slower to add a large "Release series"
  than a "Release group series", where everything comes back in one call),
  deduplicating by release group in case the same underlying album is
  listed under two different editions.
- **Prowlarr searches no longer wait on the slowest indexer Prowlarr has
  configured.** Diagnosed live: CantiNode's Prowlarr source called
  Prowlarr's own aggregate `/api/v1/search`, which doesn't respond until
  every indexer it has configured has — with 7 real indexers, 6 answered
  in under 4 seconds each, but one scraped torrent site alone regularly
  took 25-60s+, so every search waited on it. `internal/indexer/prowlarr`
  now lists Prowlarr's own enabled sub-indexers and queries each one
  individually (via Prowlarr's `indexerIds=` param) in parallel, each
  bounded by its own 20s timeout — a slow one just misses that round
  instead of blocking the rest. A new package-local backoff (mirroring
  `internal/indexer`'s existing top-level indexer backoff) rests a
  sub-indexer after 3 consecutive timeouts/failures so CantiNode stops
  even attempting a consistently bad one for a while, without needing
  Prowlarr to report anything itself — confirmed live that Prowlarr's own
  `averageResponseTime` stat is a stale, RSS-sync-dominated rolling
  average, not a reliable real-time signal. Falls back to the old single
  aggregate call if the sub-indexer list itself can't be fetched, so this
  is never worse than before.
- **Album cover art now tries TheAudioDB first, falling back to Cover Art
  Archive** for whatever TheAudioDB doesn't have — previously album covers
  came from Cover Art Archive alone. TheAudioDB is keyed by release GROUP
  (the album as a whole, its own granularity) rather than a specific
  release/edition the way Cover Art Archive is; both still cache to disk
  under the same per-release identity as before, so a release with art in
  neither source still gets a proper "no cover" answer instead of a broken
  image. `internal/audiodb` gained `LookupAlbumByReleaseGroupMBID`, and
  `internal/coverart.Client` takes an `*audiodb.Client` (nil-able, skips
  straight to Cover Art Archive).
- **Matching now knows the difference between a file CantiNode grabbed
  itself and one that was already on disk or added by hand — and uses it.**
  A completed grab already has a known target (the `wanted_albums`/`albums`
  row it was searched for), but importing it used to throw that away and
  re-derive everything blind from tags via the same fuzzy search a
  manually-placed file needs. `internal/importer` now stamps each copied
  file with `expected_release_group_mbid` before the scan that
  discovers/matches them runs; `resolveFolderRelease` uses it to skip
  straight to that release group's best-by-file-count cached edition,
  never a MusicBrainz search. This only ever skips the *album* search —
  per-track title/position verification (`matchEntriesToRelease`/
  `slotTrack`) still runs unchanged, and a safety gate refuses the
  shortcut outright if the folder's own tags positively name a different
  album than expected, so a mislabeled or wrong download still gets a
  real shot at being identified as whatever it actually is rather than
  being force-bound to the wrong thing. The field doubles as a permanent,
  queryable record of a file's own provenance (empty = found on disk or
  added by hand).
- **Compilations ("Various Artists") are now recognized even without a
  consistent `AlbumArtist` tag.** A folder was already treated as a
  compilation when every file agreed on `AlbumArtist=Various Artists` —
  that's a real, if coincidental, MusicBrainz artist identity match. The
  gap was a folder tagged only with each track's own (genuinely
  different) `Artist`, and no `AlbumArtist` at all: `folderTagConsensus`
  used to fail outright the moment artists disagreed, falling back to
  weak per-file search. It now recognizes "album title agrees, artists
  disagree, no AlbumArtist override anywhere" as a compilation signal and
  searches MusicBrainz as "Various Artists" instead of giving up.
- **Each track now keeps its own real performing-artist credit**, not
  just the album's — confirmed live against a real "Various Artists"
  compilation that MusicBrainz's own tracklist response already carries
  this (Phil Collins, Duran Duran, UB40, each correctly distinct per
  track), just nothing stored it before now. Shown on the album page only
  when it actually differs from the album's own artist, so an ordinary
  single-artist album is unaffected.
- **The Unmatched Files auto-match panel can now discover an artist that
  isn't in your library at all yet.** Its Artist dropdown was always
  scoped to `GET /music/artist` (already-added artists only) — a folder
  on disk for an artist you'd simply never added had no path forward
  there: not in the dropdown, no cached discography to match against. A
  new **"Artist not in your library?"** toggle opens a live MusicBrainz
  search right in the panel; picking a result adds and monitors it via a
  new, lighter `POST /music/artist/quick` (discography only — what the
  panel's own Album dropdown needs right away) rather than the Library
  page's full **+ Add & Monitor** (which also pre-fetches every release's
  versions/tracklists and TheAudioDB bio/photo, none of which matter to
  finish matching a file). `MetadataFetchedAt` is left unset, so the very
  next scan's existing backfill sweep catches the rest up automatically —
  no separate follow-up mechanism needed. Selects the new artist
  immediately either way, so album matching continues in the same flow.
  Never triggered automatically by Auto-match itself — adding a whole new
  artist always needs its own explicit human pick, same as "+ Add"
  always has. The automatic background scanner already handled this case
  on its own (folder-level MusicBrainz release search creates a
  never-seen artist on the fly when confident enough); this closes the
  matching-UI-specific gap for when that automatic match wasn't
  confident enough and the file fell through to manual review. See
  [Libraries](docs/libraries.md#existing-file-import-unmatched-files).
- **Multiple, freely-named root folders, with a "Move to…" action to
  relocate an artist's owned, matched files between them.** Settings → Media
  Management already allowed adding more than one music root folder, but
  they were path-only (no name) and every new automatic grab landed in
  whichever one happened to be first — with several folders configured,
  there was no way to control where new music went, and no way to move an
  artist's existing files to a different folder short of doing it by hand
  outside the app and hoping the database noticed. Now: each root folder
  has its own editable name (`PUT /rootfolder/{id}/name`), one is marked
  **default** (`PUT /rootfolder/{id}/default`) as the fallback destination
  for a brand-new artist's first grab — an artist that already owns
  matched files always has new grabs join them there instead, so an
  existing discography never splits across folders on its own
  (`internal/importer`'s new `targetRootFolder`; an artist whose files are
  *all* still unmatched is the one case this can't recognize, since an
  unmatched file isn't linked to any artist at all yet). An artist page's
  **Move to…** dropdown previews (file count, total size) then relocates
  their owned, matched files to a different root folder in the
  background, the same shape as a library scan — a pure relocation
  preserving each file's existing relative path,
  not a re-organize, since it can mean copying many GB across physical
  drives rather than a fast same-drive rename. Copy → size-verify → record
  the new location → delete the original, in that order, per file, so an
  interruption partway through never leaves the database and disk
  disagreeing about where a file lives; a destination collision is skipped
  and reported rather than overwritten, and doesn't stop the rest of the
  move. See [Libraries](docs/libraries.md#root-folders).
- **"Write tags" (and scanning generally) now covers every popular audio
  format**: MP3/FLAC as before, plus the MP4/M4A family (M4A/M4B/M4P),
  OGG/OGA (Vorbis comments), Opus-in-Ogg, DSF, and WAV. Writing routes
  through `go.senan.xyz/taglib` (upstream TagLib compiled to WASM, run
  through wazero — no cgo, no new runtime dependency) for everything past
  MP3/FLAC rather than hand-rolling each container's own binary format.
  MP4 in particular was deliberately left unsupported before now: its
  metadata atom sits before the audio data, so resizing it means
  correctly rewriting every track's chunk-offset table (`stco`/`co64`) or
  the file's audio silently points at the wrong bytes — exactly the kind
  of mistake a mature, extensively-used library is worth depending on to
  get right rather than re-deriving under time pressure. WAV needed a
  matching change on the *read* side too — `dhowden/tag` (everything
  else's tag reader) opens a WAV file without erroring but returns "no
  tags found" for every one tested, so `internal/tagreader` now reads WAV
  through the same taglib backend; Opus-in-Ogg turned out to need nothing
  extra, since `dhowden/tag` already parses `OpusTags` correctly (as an
  alias of the byte-identical `VorbisComment` structure) despite this
  project's own now-corrected doc comment once claiming otherwise. The
  album page's "write tags" button is hidden for any file format neither
  backend covers, instead of only failing after the click. See
  [Fixed](#fixed) below for a real bug this surfaced in a dependency.
- **Four quality-of-life items from a code-review-driven pass, all live
  2026-08-13:**
  - **Upgrades allowed" grabs now auto-swap the old file** instead of
    leaving it alongside the new one forever. `grabs.upgrade_album_id`
    (migration 024) ties an upgrade grab to the album it's for, mirroring
    `wanted_album_id`'s tie to a wanted album; once `internal/importer`
    scans a completed upgrade grab in, it deletes the old file for every
    track that just gained a genuinely new matched one — track-by-track,
    so a partial or failed match on the new release can never leave a
    track with nothing.
  - **The Library grid can be sorted** (name, recently-added, album count,
    missing count), not just filtered by name — the same `SortSelect`/
    `DirectionButtons` control an artist's own Albums grid already uses.
  - **"Add artist" search results are a poster-card grid** now, matching
    the Library grid and an artist's Albums grid instead of a bare
    name-plus-button list. MusicBrainz's artist search returns no image,
    so every card falls back to the same lettered tile used elsewhere when
    there's no cover art — but the search response now also surfaces
    type/country/disambiguation, shown as the card's subtitle, to help
    pick the right artist among same-named results.
  - **A failed grab can be retried straight from Activity** — "Search
    again" on a failed history row expands the same `ReleaseBrowser` the
    album page itself uses, instead of sending you off to find the album
    by hand.
- **Release-version selection, fuller metadata caching, and multi-disc-
  aware auto-matching.** The matching UI can now pick a specific
  MusicBrainz release (pressing/edition) instead of always matching
  against one fixed "representative" release — every known version's
  metadata and full tracklist is cached (`release_group_versions` +
  per-release `release_tracklist_cache`, replacing the old single-release
  scheme) the first time an artist's discography syncs, with a backfill
  sweep on every scan catching artists added before this existed. Artist
  genres/tags/community rating are now cached from MusicBrainz alongside
  the existing bio/photo, even though nothing displays them yet. Removing
  an artist now actually purges its cached version/tracklist metadata and
  on-disk cover art/photo (previously orphaned forever); removing a single
  album correctly leaves all of that alone. Cover art now resolves for
  wanted/missing albums too, not just owned ones. The scanner detects
  CD1/CD2/Disc-N sibling subfolders of the same album and merges them into
  one matching group — tolerant of per-disc Album-tag suffixes ("Album CD
  1" vs "Album CD 2") — while correctly leaving a discography/box-set
  bundle of genuinely different albums ungrouped; purely a matching-time
  regrouping, never touches files on disk. The unmatched-files page's
  auto-match panel gets a version dropdown and an **Auto-match** button
  that confidently pre-fills artist/album (fuzzy match against the
  library, threshold tunable via the new **Auto-match dropdown
  confidence** setting, default 85%) and version (file-count match) — the
  human still reviews and approves every suggestion before anything
  commits, same as before. See [Libraries](docs/libraries.md#existing-file-import-unmatched-files)
  and [Configuration](docs/configuration.md#music-matching).
- **Automatic wanted-list sweep is back** (`internal/autosearch`): a
  background loop, mirroring `internal/importer`'s own shape, searches and
  grabs every **monitored** artist's still-wanted albums on a timer
  (default once a day, tunable via `wantedSearchIntervalMinutes` on
  `GET/PUT /settings/timings`, or **Settings → General → Advanced:
  background timings**), picking the best release that clears the active
  quality profile exactly like a manual grab would. Unmonitored artists'
  wanted albums are never swept automatically — search/grab stays entirely
  manual for them, matching the existing decoupling between wanting an
  album and monitoring its artist. This is the other half of Completed
  Download Handling's return (below): that restored automatic *import*,
  this restores automatic *search*.
- **Quality profiles' "Allow upgrades" toggle now does something.**
  `internal/release/score.go` already had upgrade-aware rejection logic
  (`MinFormatScore` — reject anything not a genuine step up), but nothing
  in the app ever called a search with an owned format to check against;
  the toggle looked live in Settings but had no effect. A **Search
  upgrade** button now appears on an already-owned album's own page once
  its format hasn't met the profile's cutoff (`GET/POST
  /music/album/{id}/upgrade/search|grab`) — deliberately manual-trigger
  only, not folded into the automatic sweep above, since a background loop
  silently grabbing a second copy of something already owned felt like it
  needed a human in the loop. A grabbed upgrade lands alongside the old
  file, not in place of it; removing the old one is still a manual
  per-file action.
- **Owned and wanted albums share one grid.** The artist page's separate
  owned-albums poster grid and Wanted list are unified into a single
  **Albums** grid, badged owned/wanted/downloading — mirroring how a
  library-member book shows up whether or not it's been downloaded yet.
  Clicking a wanted album opens an inline panel with **Search
  releases**/**Stop wanting**; search only fires once you actually click
  **Search releases**, not just from opening the album (matching how the
  book UI this was ported from behaves).
- **A rich release browser** (`ReleaseBrowser.tsx`, shared by the wanted-
  album and album-upgrade search flows): approved/all toggle, protocol
  filter, sort by score/size/seeders/age, score/format/retail/pack pills,
  and — for a rejected release — its rejection reason shown inline with a
  "grab anyway" override. Replaces the previous bare title-plus-Grab-button
  list; reuses CSS that had been sitting in `App.css` unused since the
  original LibriNode-fork port.
- **Per-album Scan files, Organize…, and Remove album** actions, alongside
  the existing artist-level ones. Unlike the artist/library-wide scan
  (which walks every root folder regardless of where it's clicked from),
  the album-level scan genuinely only walks that one album's own folder —
  useful for picking up a manually-dropped-in file without paying for a
  full library walk, and it never touches a sibling album's records even
  though it has no root-folder-wide context to lean on for that. Remove
  album takes the same optional `?deleteFiles=true` the artist-level
  remove does.
- **Completed Download Handling is back** (`internal/importer`): a background
  loop polls every in-flight grab against its download client every 2
  minutes (immediately on startup too), and once one reports done, copies
  its files into the music library and scans them in — no more manually
  remembering to re-scan after a grab finishes. A release's remote path
  (as its download client reports it) is translated through **Settings →
  Download Clients → Remote path mappings** first, so a debrid bridge or a
  client on another machine resolves correctly. A grab is now linked to
  the wanted album it was made for (`grabs.wanted_album_id` — renamed from
  the dead `book_id` leftover the ebook/comic removal left behind; see
  migration `021_grabs_wanted_album.sql`), so a successful import marks
  that album **downloaded** instead of leaving it stuck at "downloading"
  forever with no way back — the queue/history API field is renamed
  `bookId` → `wantedAlbumId` to match. Once a download is safely copied
  into the library, it's removed from its download client (with its data)
  so it doesn't sit around in the download folder or the client's history
  forever — and since some clients (debrid bridges in particular)
  acknowledge that removal but don't actually honor the delete-files flag,
  the importer also deletes the source directly afterward as a fallback,
  guarded against ever touching a suspiciously shallow path (must be
  absolute and at least three segments deep, so a bad or mistranslated
  path can never wipe a mount root or top-level directory). A
  client-reported failure, a failed copy, or a grab that vanishes from its
  client's queue outright (removed there, or lost to a restart) reverts
  the wanted album back to **wanted** instead — either way, nothing is
  deleted until CantiNode's own copy is confirmed good. Live-verified
  against a real TorBox/SABnzbd bridge, including the debrid-bridge
  quirk above: the bridge accepted the delete-files request but left the
  files in place, exactly the case the direct fallback exists for. This
  restores the half of Completed
  Download Handling dropped in the ebook/comic removal that doesn't need
  an automatic search sweep to be useful on its own; see the
  [roadmap](ROADMAP.md#future-) for the other half.
- **A "Music" settings tab** (`Settings → Music`) wires up MusicBrainz/
  TheAudioDB configuration that already existed on the backend
  (`GET/PUT /settings/music`) but had no UI at all: a MusicBrainz contact
  email (recommended by their API usage policy), a TheAudioDB API key
  (optional — falls back to their shared public test key), the
  **organize on match** toggle, and the **minimum match confidence**
  slider. The same page gets a **Clear image cache** button, wiring up
  the also-previously-orphaned `DELETE /cache` endpoint.
- **Quality profiles gained size bounds and a fuller language list.**
  Min/max release size (in MB) was always scored (`internal/release.Score`
  rejects releases outside it) but had no form field — a new profile
  silently inherited the backend's generic 20 KB–500 MB fallback, which
  rejects most real FLAC albums; the form now shows and edits it directly,
  defaulting to 1 MB–4 GB (matching the seeded "Standard Music" profile).
  The language dropdown grew from 5 to the full 10 languages
  `internal/release.Parse` recognizes.
- **A "pack" badge on releases in the search browser** flags one that looks
  like it bundles multiple books/volumes — an explicit volume span
  ("v01-v12"), a self-declared complete run/collection ("Complete Series",
  "Box Set"), or now also a release that simply names a *second* book by the
  same author alongside the wanted one ("Tau Zero & The Boat of a Million
  Years") — the two-title case doesn't use any "Complete"/"Collection"
  wording at all, so it needed its own check against the author's other
  titles. Shown before you grab it. Hover it to see the volume range when
  there is one.
- **Audiobooks now support multi-book pack imports**, matching the existing
  ebook/manga/comic behavior. A bundle that organizes each book into its own
  top-level subfolder ("Author - Series Collection Unabridged/Book 1/",
  "Book 2/", …) fills the grabbed book (matched by **folder name**, never
  size — the largest set of tracks in a bundle is rarely the book you
  grabbed) plus every other book the bundle matches, same as ebook/manga/comic
  packs — with **Import whole packs** (on by default) governing whether that
  includes unmonitored books too, or only ones already monitored. The
  detection is deliberately conservative: it only activates for two or more
  distinctly-named top-level folders with nothing loose sitting in the root —
  anything less structured (a single folder, flat files, disc/part subfolders
  like CD1/CD2 at the root) falls back to the existing single-book behavior
  unchanged, and a bundle whose folder names don't match the grabbed book by
  title falls back to treating the whole download as that one book rather
  than guessing wrong.
- **A "Source" button on each release in the search browser** opens the
  release's own listing/info page in a new tab (indexer feeds carry this as
  the standard Torznab/Newznab `<comments>` field; native sources set it from
  their own listing/file page). Only shown when a release actually carries
  one.
- **AudioBook Bay results carry file size and posted date** straight from the
  search listing (no extra request needed), and the release scorer's author
  check now falls back to a post's own tag list when a series/collection
  post's title omits the author — a bare "Dune Messiah" post used to always
  reject with "does not mention the author" even though ABB's own Keywords for
  that post name the author directly.
- **A "direct" protocol filter** joins usenet/torrent in the release browser —
  named after the shared backend protocol rather than any one indexer, so it
  covers Library Genesis today and any future direct-download source for free.
  The filter bar now lists whichever protocols are actually present in the
  results instead of assuming exactly two.
- **Sort controls on the Books, Wanted, and Missing lists.** A compact ⇅
  dropdown in each section header re-orders it by title, release date, or rating;
  Missing keeps its series grouping as the default (choose a sort to flatten it),
  and Wanted defaults to recently-added — each section's existing look is its
  default, so nothing changes until you pick a sort.
- **A "Refresh all metadata" button** under Settings → Metadata → Cache
  maintenance re-fetches every author and series from your provider in one
  action — descriptions and covers come back, and (with the reconcile) entries
  the provider no longer lists are removed. Previously a full re-sync could only
  be triggered per-library from each library page or waited for the daily sweep;
  the metadata section, where you go to clear/rebuild metadata, now offers it too
  (`POST /library/refresh` accepts `{"mediaType":"all"}`).
- **Box sets & collections are hidden from metadata search by default**, with an
  opt-in under Settings → Metadata ("Show box sets & collections"). Hardcover
  lists omnibus/box-set editions alongside the individual books — six of them for
  a "dune" search — so results stay to single books unless you want the sets.
- Organize now **scans first** (scoped to its level — a library page scans
  only that library's roots, an author/series page its own format) so the
  move plan always reflects what's actually on disk, and library-level
  organize gained a **cleanup**: files that don't belong in the library
  (download junk like `.nfo`/`.torrent`, or another type's media dumped in
  the root) are previewed with a delete checkbox, and applying prunes every
  empty folder. Matched files, unmatched media, `.opf` sidecars, artwork,
  and `ComicInfo.xml` are always kept; deletions are re-validated
  server-side against the library's own roots.
- **Library Genesis** ebook source (native indexer, `direct` protocol).
  Searches libgen.li and downloads by MD5 through its `ads.php` → `get.php`
  mirror — the direct fetcher follows the landing page to the real file — so
  downloads work **without any account**. Each result carries its author, year,
  language, and file format from the results table, so an interactive or
  automatic search keeps only the book you asked for in the language and format
  your quality profile wants (a wrong-language edition, wrong-author book, or an
  unwanted format is filtered out; the release scorer's author check is now
  order-independent so "Last, First" listings match). Off by default,
  user-added, user-responsible. (Anna's Archive was evaluated and dropped — it
  renders search behind a JS/anti-bot wall needing a browser/bypasser CantiNode
  doesn't ship, and Libgen is the catalog it aggregates.)
- **Light theme** with a sidebar theme control: Auto (follows your OS, live),
  Light, or Dark — a per-browser preference applied before first paint, so
  there's no flash. The whole UI runs on one CSS-variable contract; the light
  palette is paper-toned.
- **Per-file actions** on book and volume detail pages: every file row now has
  **organize** (preview → confirm the naming-template moves for that book) and
  **delete** (removes the file from disk after a confirmation) — no more
  round-trip through library-wide organize or the API.
- Mobile: sidebar group labels now render as row headers at the narrow
  breakpoint instead of disappearing.
- A general **`direct` download protocol** — a third release protocol beside
  torrent and usenet: a built-in download client (type `direct`, its "host" a
  local download folder) where **CantiNode streams the file itself** —
  mirror-list failover, following an open-mirror landing page (or a membership
  API's JSON answer) one hop to the real file, live progress in the queue, and
  Completed Download Handling importing the result like any other grab. Library
  Genesis rides it; any direct-link source can.
- Native indexer framework + **AudioBook Bay**. A new `type: native` indexer
  kind sits beside Newznab/Torznab: a built-in Go source, selected as the
  indexer's type (no URL), feeding the same search/scoring/grab pipeline as the
  API clients. Native sources are CantiNode-managed only and hidden from
  Prowlarr, so it never treats them as indexers it owns. The first source is
  AudioBook Bay: it has no API, so it scrapes the listings and **assembles the
  magnet from the on-page info hash + tracker list**, yielding an ordinary
  torrent that rides the existing qBittorrent path. These are dual-use
  shadow-library sources — **nothing is bundled or enabled by default**; a user
  adds one deliberately and is responsible for its use. (A general `direct`
  HTTP-download protocol and Library Genesis followed.)
- ISBN / embedded-metadata / fuzzy file matching for library scans. On top of
  the existing exact author/title matching, a file now also matches by **ISBN or
  ASIN** — parsed from the filename or read from an epub's embedded OPF metadata
  — against a known edition, so a correctly-identified but oddly-named file is
  placed outright (ISBN-10 and ISBN-13 fold to one checksum-validated form). And
  when nothing matches by title, a **fuzzy** pass (character-bigram similarity)
  pre-fills the Unmatched card's import picker with the closest book — offered
  for one-click confirmation, never auto-imported. A file with neither a usable
  identifier nor a fuzzy hit behaves exactly as before.
- Automated clean-machine backup/restore drill: a backup taken on a populated
  data dir is staged into a brand-new empty one and swapped in through the real
  startup path, asserting the library comes back whole (author/book rows intact,
  config verbatim) and that a fresh machine — with no live files to protect —
  is left with no `.restore`/`.pre-restore` leftovers. Closes the clean-machine
  half of the restore drill that the staged-restore test didn't cover.
- Automated upgrade-path (migration) testing: a database seeded at an older
  pre-rebuild schema is driven through the full remaining migration chain,
  asserting that the table rebuilds keep every row and the membership/variant
  backfills compute the values an upgrading user expects — migration bugs are
  data-loss bugs, so they now fail a test instead of a real library. A
  companion test confirms a fresh database applies every migration and that
  re-opening it is a clean no-op.
- Metadata fallback providers: **Open Library** and **Google Books** ship as
  keyless book providers (a Google Books API key is optional, only to lift
  rate limits). Configure them, in order, under Settings → Metadata →
  Fallbacks: the active provider answers everything it can, and a fallback is
  consulted only when the active one finds nothing for a search or an id
  lookup — the "as fallbacks" contract, not a merge. A record found through a
  fallback is stored under that fallback's name, so its later metadata refresh
  routes back to the same source rather than the primary that never had it
  (a new `metadataSource` field carries the origin through the add). Either
  new provider can also be selected as the primary book provider. Implemented
  as a `metadata.FallbackProvider` chain wrapping the registered providers, so
  further sources are still one registration away.
- Roles / permissions: every login account is now an **admin** or a
  **member**. Members get everyday use — browsing, monitoring, search, grab,
  scan, organize, and their own password — but not the server's own
  configuration (Settings, Indexers, Download Clients, Quality Profiles,
  backups, logs, root folders) or other accounts. Every configuration and
  account-management route is gated behind an admin check on the backend (the
  UI only hides what the API already refuses); the API key stays
  admin-equivalent for automation. Set the role when adding a user or
  promote/demote later. The default user is always an admin, so an instance
  can never be left with no one who can administer it, and changing a role
  revokes that account's other sessions immediately. Accounts created before
  roles existed migrate to admin, so nothing changes until you choose to
  restrict someone.
- Library-wide "Refresh metadata" on every library page (except organize-only
  magazines) — the bulk twin of the per-author/per-series Refresh buttons,
  honoring per-record provider overrides; runs in the background, one at a
  time, and reports how many records it covers.
- Remote path mappings (Settings → Download Clients): map a download
  client's reported path prefix to the local path where this server sees
  the same files — longest prefix wins, boundary-aware, case-insensitive,
  separator-converting. Ends the "mount the share at the exact same path"
  requirement for remote/containerized clients.
- Security hardening: login sessions are bound to their account (user
  removal and password changes revoke them immediately; disabling login
  revokes all), the API-key check is constant-time, and the image proxy
  refuses to reflect non-http(s) URLs.
- Series pack grab: manga/comic series pages get "🎁 Search packs" — release
  parsing understands volume ranges ("v01-v41", "#1-60") and completeness
  words, pack candidates are ranked (full range > partial > bare series
  title; single volumes rejected back to the per-volume flow), and a grabbed
  pack is imported by the existing pack importer, filing every matching
  volume. Closes the "series torrents are whole-series packs" content gap.
- Configurable background timings (Settings → General → Advanced): wanted
  search, metadata refresh, health checks, and import polling — blank means
  default, values are clamped to sane ranges, applied at startup.
- Bulk monitor from Missing on author and series pages: per-row checkboxes
  with "+ Monitor selected", plus "+ Monitor all" per series group or whole
  section.
- Activity History paging: server-side total with progressive "Show more"
  (the 200-row cap is gone) and a debounced title filter.
- Friendly first-use states: an empty library now shows per-type guidance
  with direct + Add / Scan actions instead of one line of text.
- Responsive pass (≤700px): wrapping card heads and action rows, full-width
  settings fields, adaptive poster grids, bottom-sheet toasts; plus an
  accessibility edge pass (aria-current nav, labeled icon-only controls,
  meaningful alt text on detail art).
- White-glove P3 surface passes: quality profile formats are ordered chips
  (reorder/remove/add with per-type suggestions); indexer and download
  client rows show protocol/priority/disabled pills with the URL beneath;
  calendar items are clickable (through new authorId/seriesId on the
  calendar API) with relative when-badges on day headers; the System page
  leads with a status tile grid and colors ERROR/WARN log lines; sign-in
  and API-key screens are centered branded cards; author and series pages
  show an owned/total progress meter.
- White-glove UX wave: an app-wide toast layer (stacking, dismissible)
  replaces the top-of-page error card for in-app errors; every native
  browser confirm() popup (13) is now a styled confirm dialog; search-and-add
  renders provider results as poster cards with cover art and per-card add
  state; URL routing (hash-based) makes every page bookmarkable with working
  back/forward and refresh; indexers, download clients, and quality profiles
  are editable in place (including the 1–50 priority, finally exposed);
  global search in the sidebar spans every library; skeleton loading states
  replace "Loading…" text; Activity history, the blocklist, and backups show
  relative ages via shared date/size formatting utils.
- Existing-file import across all five libraries: unmatched files get a
  best-guess suggestion with a 0–100% confidence rating, one-click Import and
  bulk "Import all matched", duplicate resolution (both files shown, Replace
  or Delete — variant-aware for manga), and one-click adding of a missing
  author (provider search), manga/comic series (provider search), or magazine
  (by name). Magazine imports materialize the issue on the spot; adopted
  prose books are enrolled and monitored.
- First-run setup wizard: a fresh instance is claimed by its first visitor —
  create the account, then guided steps for library folders (with a visual
  folder browser), the Hardcover token, an indexer, and a download client.
  No API-key paste required.
- Multi-user accounts: the Security card lists users with add, change
  password, make-default, and remove (the default account is protected).
- Visual folder picker for root folders (Settings and the wizard): browse the
  server's filesystem instead of typing paths blind.
- Release browser: interactive search results in one organized component —
  approved/all and protocol filters, sorting (score/size/seeders/age),
  always-visible size/seeders/leechers/age per row with the active sort
  highlighted, rejection reasons inline with a "grab anyway" override, and
  per-row grab feedback.
- Live download progress: Activity queue rows carry progress bars and per-line
  remove; blocklist and history collapse into dropdowns; a book page's badge
  turns "downloading N%" while its grab is active and reverts on its own;
  series volume rows and Wanted cards show the same live state.
  Queue responses are served from a shared 15-second snapshot so open pages
  never stampede the download clients.
- Richer default file naming across all libraries: per-book ebook folders,
  author/title/year in file names, zero-padded volume/issue numbers.
- Home page tiles link to their book or series page; the book page's
  cross-format badge links to the same book in the other library; multi-file
  audiobooks list their tracks on the book page.
- Junk/spam defense: releases naming an executable are rejected before grab;
  completed downloads with wrong content (or an executable payload) are
  blocklisted, deleted from the client *and* disk, and immediately re-searched;
  completed-but-never-readable paths are abandoned after a grace period.
- Import handling options (Settings → Download Clients), all on by default:
  import whole packs, remove completed downloads from the client, delete the
  downloaded files after import.
- Debrid-bridge support (Real-Debrid/TorBox style): CantiNode resolves
  releases on its own side — NZB fetched and uploaded via SABnzbd `addfile`,
  torrents passed as magnets or uploaded `.torrent` bytes — and tolerates
  slow bridge adds by confirming against the client's list.
- Multi-disc audiobooks: disc subfolders (CD1/Disc 02/Part 3) survive import
  and scan as one book unit; other nesting is flattened collision-safely.

### Fixed
- **A missing album cover stayed missing forever, even after Cover Art
  Archive later got the artwork.** Found live: a "Cities 97 Sampler"
  volume's cover was genuinely unavailable when first checked (Aug 16),
  cached as a permanent "no cover art" sentinel — but Cover Art Archive's
  own catalog isn't static (community members add art to a release after
  the fact), and the art was actually there by the time this was
  reported, live-confirmed via a direct request. Sentinels now expire
  after 30 days (`noCoverRecheckAfter`); one older than that gets a real
  live recheck on the next request instead of serving the stale miss
  forever, and a still-genuine miss simply rewrites the sentinel,
  resetting its own clock for another 30 days.
- **A Various Artists compilation track could silently, permanently file
  under its own real per-track performer instead of "Various Artists" —
  with no error anywhere to notice.** Found live: "Little Feat," off a
  Cities 97 Sampler volume, had its own separate artist entry despite
  MusicBrainz's real data for that exact release being fully correct
  (confirmed via both the batch and single-lookup endpoints after the
  fact). Root cause: `correctArtistCreditForCompilation`'s own
  `LookupReleaseWithTracklist` fetch — the one that substitutes the
  release's real "Various Artists" credit — degraded silently to the
  recording's own per-track credit on any fetch failure (a transient
  MusicBrainz hiccup, not a data problem), producing a full-confidence
  WRONG match with nothing recorded anywhere. Worse, once matched, that
  wrong album's own `mbid` then permanently "won" any later correction
  attempt too, via the `ON CONFLICT(mbid)` recovery above — the mistake
  was self-reinforcing. A genuine correction-fetch failure now makes the
  direct-match fast path decline (the same auto-route an embedded-tag
  inconsistency already used) instead of silently locking in the wrong
  artist, giving the file a real second shot via folder consensus — or,
  if nothing can resolve it, a real, visible scan error instead of a
  confident wrong answer.
- **A batch of tracks across two "Cities 97 Sampler" volumes crashed
  outright with `UNIQUE constraint failed: albums.mbid` instead of
  matching.** Found live in a scan's own error list right after the fixes
  above. `GetOrCreateAlbum` only ever checked for an existing row scoped
  by `(artist_id, release_group_mbid)` — but the `mbid` column carries its
  own, separate, database-wide UNIQUE constraint it never checked first.
  Two tracks of the same physical release can resolve, via
  `Recording.BestRelease`, to different release-group MBIDs (the same
  class of real MusicBrainz data quirk the earlier Birdy/duplicate-
  recording fix already found in this catalog) — so the second track's
  lookup found nothing and its plain insert collided on the raw `mbid`
  column. The insert now goes through `ON CONFLICT(mbid) DO NOTHING`,
  atomically, and always reads the row back afterward — whichever call
  claimed that mbid first, every later one just reuses it instead of
  crashing.
- **A Various Artists compilation folder's tracks left Unmatched one at a
  time, several seconds apart, instead of together.** Reported live while
  watching an actual scan — visible even after the batched-recording-
  lookup speedup above, since that only batches the recording IDs
  themselves. Root cause: every track in a compilation resolves to the
  exact same release, but `correctArtistCreditForCompilation`'s own
  `LookupReleaseWithTracklist` fetch (needed to get the release's real
  "Various Artists" credit) ran once per track with no cache at all — an
  N-track folder paid N identical network fetches for data that's the
  same on every one. `matchFolder` now shares one `releaseCreditCache`
  across every file it processes, so a release is only ever fetched once
  per folder no matter how many tracks resolve to it.
- **A direct-MBID file could sit unmatched forever if its embedded
  recording was missing from a batched lookup.** Reported live: a
  perfectly valid, catalog-present recording ("Train" - "I Am", off a
  "Cities 97 Sampler" volume) came back completely absent from an
  18-recording `rid:(...)` batch search that correctly returned the
  other 17 — confirmed directly against the real MusicBrainz API: the
  same ID returns zero results from the search endpoint but resolves
  fine from the authoritative per-ID lookup endpoint. The batch-lookup
  speedup (above) was treating any ID missing from the batch response as
  "doesn't exist," the same outcome as a real 404 — silently leaving a
  perfectly valid file unmatched. `matchDirectEntries` now gives a batch
  miss one more authoritative shot via the single-lookup path
  (`matchFileDirect`) before giving up, at the cost of one extra request
  only for the rare miss.
- **Some Various Artists compilation tracks still filed under their own
  real performer instead of the shared "Various Artists" artist.**
  Reported live: Gary Moore and Rachel Platten tracks off different
  "Cities 97 Sampler" radio-station compilation volumes each got their
  own separate library artist entry. Root cause: `correctArtistCreditForCompilation`
  only substituted the release's own artist-credit when the release
  group's MusicBrainz `SecondaryTypes` said `"Compilation"` — but that
  sampler series is tagged `"Live"` instead, even though its
  release-level artist-credit is correctly "Various Artists" (confirmed
  live against the real API). `releaseNeedsArtistCreditCheck` now
  triggers on any release group that isn't a plain single-artist studio
  album, not just ones explicitly tagged "Compilation" — a false
  positive (an ordinary solo Live album/EP/soundtrack) just costs one
  extra harmless MusicBrainz request, never a wrong result.
- **The "Various Artists" pseudo-artist was flooding Missing with tens of
  thousands of bogus entries.** Any compilation track files under
  MusicBrainz's own special "Various Artists" artist (a real, universal
  MBID, not one CantiNode assigns) — but that artist's "discography" is
  every multi-performer compilation MusicBrainz has ever cataloged, not
  anything meaningful to list as Missing. Confirmed live: browsing it hit
  MusicBrainz's own rate limit partway through and had left 10,000
  cached "missing" release-group rows from an earlier, pre-limit run —
  the highest count CantiNode's own pagination ceiling allows.
  `discography.Service.RefreshArtist` now special-cases
  `musicbrainz.VariousArtistsMBID`: skips the browse entirely and clears
  any stale cached rows, so it self-heals on its own next refresh (a
  scan, the metadata backfill sweep, or an explicit "Refresh metadata")
  with no manual cleanup needed.
- **A Various Artists compilation organized into a folder per track's own
  performer instead of one shared "Various Artists" folder.** Reported
  live: `{Artist}/{Album}/...` was resolving `{Artist}` to each track's
  real performer (Phil Collins, Duran Duran, …) rather than the release's
  own "Various Artists" credit. Root cause: `applyMatch`'s artist
  assignment used the matched recording's own artist-credit — the real
  per-track performer — for filing, which is identical to the release's
  own credit on an ordinary album but diverges exactly on a compilation.
  Whole-folder matching (`matchEntriesToRelease`) already substituted the
  release's own credit before assignment; the three per-file paths
  (`matchFileDirect`'s embedded-MBID fast path, `matchFileFuzzy`'s
  standalone fallback, and `ManualMatch`) didn't, so a well-tagged
  compilation rip — each file already carrying its own correct recording
  ID, which bypasses whole-folder reasoning entirely — hit this on every
  track. `correctArtistCreditForCompilation` now applies the same
  substitution there too, while still preserving each track's own real
  performer as its display credit.
- **A release for a completely different artist could get auto-grabbed as
  long as it shared a word with the wanted album.** `release.Score` never
  compared a candidate's own title against who was actually being
  searched for — only format/size/health. Found live: an automatic sweep
  for Avantasia's wanted album "Moonglow" grabbed **Nat King Cole**'s own
  unrelated album of the same name, purely because it was FLAC with
  usenet availability. Score now rejects a release whose title doesn't
  plausibly name the wanted artist at all (exact-phrase match, falling
  back to the artist's most distinctive word — tolerates real naming
  variance like "Tobias Sammets Avantasia" for "Avantasia" without a full
  fuzzy pass); "Various Artists" always passes, since a compilation's own
  file/torrent name essentially never states that phrase literally.
  Applies everywhere scoring runs: manual search, upgrade search, and the
  automatic wanted-list sweep alike.
- **An album could vanish from the library entirely — not owned, not
  wanted, not missing — once its last file disappeared.** Matching
  creates the `albums` row up front and converts the matching
  `wanted_albums` row away immediately; if every one of that album's
  files was later cleared, deleted, or went missing on disk (moved
  outside the app, a bad download, etc.), the now-empty `albums` row
  stuck around forever: excluded from Owned (needs a linked file),
  excluded from Missing (an `albums` row already exists), and no longer
  in Wanted either. `ReapOrphanedAlbum` now deletes that empty row
  wherever a file can be an album's last one — `ClearMatch`,
  `DeleteTrackFile`, a full scan's `DeleteTrackFilesMissing` reconciliation,
  and an album-scoped `ScanAlbumFolder` prune — so the release group falls
  straight back into Missing instead of going dark.
- **Write tags dropped the real per-track artist credit on a Various
  Artists compilation.** `WriteTags` always embedded the album's own
  artist row (e.g. "Various Artists") as both `ARTIST` and `ALBUMARTIST`
  for every track, ignoring the real per-recording performer
  (`track.ArtistCredit`) already stored and already shown correctly on
  the album page. Now falls back to `track.ArtistCredit` for `ARTIST`
  when it's set, leaving `ALBUMARTIST` as the compilation identity — an
  ordinary single-artist album is unaffected (`ArtistCredit` is empty
  there). See [Libraries](docs/libraries.md#existing-file-import-unmatched-files).
- **Six real bugs in the same night's grab-provenance/Various-Artists/
  per-track-credit feature, found bug-hunting it before anyone hit them
  live**: the safety gate meant to refuse a mislabeled grab
  (`resolveExpectedRelease`) only checked for a folder's tags disagreeing
  with the *expectation* — files whose own Album tags disagreed with
  *each other* (a folder that can't even agree what album it is) fell
  through the same "nothing to contradict" path as an untagged file and
  would have been force-bound to the wrong release group anyway; a new
  `albumTagsDisagree` pre-check catches that case specifically. Clearing a
  match (`SetTrackFileMatch`/`ClearMatch`) never reset
  `expected_release_group_mbid`, so the very next scan silently reapplied
  the exact match a user had just cleared — clearing now also resets the
  stamp. `SeedExpectedReleaseGroup` did a separate get-then-insert-or-
  update with no transaction, letting two concurrent imports for the same
  path race into a duplicate-path insert error; it's now one atomic
  `INSERT ... ON CONFLICT(path) DO UPDATE`. The Various Artists inference
  triggered off just two distinct per-track artists with no `AlbumArtist`
  override, letting a single mistagged or "feat."-credited track turn an
  ordinary single-artist album into a false compilation match; raised to
  three. The importer's `seedExpectedReleaseGroup` re-walked the
  destination directory a second time after `copyTree` had already walked
  it once to do the actual copy; `copyTree` now returns the paths it
  copied directly, so the second walk is gone. A weak test assertion
  (`&&`/`||` operator-precedence) in `TestPickBestVersionByFileCount`
  accepted either candidate on a file-count tie instead of actually
  proving the tie breaks toward the representative version.
- **Nine real bugs in the same night's root-folders/Move feature, found
  bug-hunting it before anyone hit them live**: a concurrent library scan
  and artist move shared no lock at all — both touch the same
  `track_files` rows, and a scan's `DeleteTrackFilesMissing` racing a
  move's `SetTrackFileLocation` could lose or duplicate a row, so each now
  refuses to start while the other is running. Deleting the current
  default root folder left *zero* folders marked default, with nothing to
  notice or fix it (`DeleteRootFolder` now promotes another remaining
  folder atomically). Adding a root folder's "become default" logic was a
  separate, non-transactional check-then-update — two concurrent adds to
  an empty table could both lose that race and leave no default
  (`CreateRootFolder` now does insert + conditional default-assignment in
  one transaction). A move whose file copy succeeded but whose database
  update then failed left the file permanently stuck — every retry hit
  "destination already exists" forever, since nothing on disk or in the
  database ever changed; it now rolls the file back to its original
  location on a database-write failure, so a retry starts clean. The move
  preview endpoint never checked artist existence, silently returning an
  empty-but-200 plan for a bad id instead of a 404. The artist page called
  the admin-only `GET /rootfolder` unconditionally, 403ing — with a
  visible error toast — on every single artist-page visit for a non-admin
  account; now gated on being an admin, matching root-folder management
  being admin-only everywhere else. A whole-move failure (e.g. the
  destination folder vanishing mid-move) set an error on the backend that
  the frontend never read, so the UI just showed "Moved 0 file(s)" with no
  explanation; also replaced the move-status poll's free-standing
  recursive `setTimeout` (no cleanup on unmount) with a proper
  `useEffect`+`setInterval`, matching the pattern already used elsewhere
  in this app. See [Libraries](docs/libraries.md#root-folders).
- **A second real `dhowden/tag` MP4 bug, found bug-hunting the new tag
  code above before anyone hit it live**: `metadataMP4.fileType` is
  initialized to `UnknownFileType` (`""`) in `ReadAtoms` (mp4.go) and
  never assigned anywhere afterward, so `FileType()` — and therefore
  every M4A/M4B/M4P file's reported format — was silently `""`, not
  `"m4a"`. Confirmed both directly against the pinned dependency source
  and empirically against a real fixture. That empty string flowed
  straight into the `track_files.format` database column and from there
  into the album page's own "write tags" button gate, which hid the
  button for exactly the file format this whole feature was built to
  support. `internal/tagreader` now falls back to the file's extension
  only when content-detection comes back with nothing, preserving the
  original resilience for every format `dhowden/tag` genuinely does
  detect (a wrong extension on an MP3/FLAC/OGG/DSF file still reports its
  true, sniffed format). The same pass also found the Ogg-family entries
  in the frontend's format allowlist (`oga`/`opus`) were unreachable dead
  code — `dhowden/tag` reports every Ogg-container file as `"ogg"`
  regardless of its actual extension — and removed them.
- **A real bug in the `dhowden/tag` dependency's MP4 parser, found while
  testing the new M4A tag-writing support above**: `readCustomAtom` (its
  freeform/`----` iTunes-atom reader — exactly how a custom tag like
  "MusicBrainz Album Id" is stored on MP4/M4A) under-skips the atom's
  8-byte type+locale header by 4 bytes, so every custom atom's value came
  back with 4 leading NUL bytes still attached. Confirmed byte-for-byte
  against a file `go.senan.xyz/taglib` wrote with the correct, standard
  8-byte header — this was purely a read-side mis-parse, not a writer bug.
  Can't patch a vendored dependency in place, so `internal/tagreader`
  trims leading NUL bytes off every extracted MusicBrainz ID as a targeted
  workaround (harmless everywhere else this code path runs — a legitimate
  Vorbis comment value has no reason to ever start with a NUL byte).
- **An artist's release-group list, and now also a release group's own list
  of editions/pressings, were silently capped at MusicBrainz's default page
  size (25/page).** The artist-discography cap was fixed first
  (`BrowseArtistReleaseGroups`, replacing `LookupArtist`'s `inc=release-groups`
  sub-resource — verified live: Boards of Canada went from a capped 25 to
  its real 41, Sex Pistols to 126); a full code-review pass afterward found
  the release-version picker's own release-list fetch
  (`BrowseReleaseGroupReleases`) had the exact same bug, fixed the same way.
  Both now paginate fully.
- **`release_group_versions` rows carried over from the old single-tracklist
  cache scheme (migration 022) could be mistaken for a genuinely-fetched,
  just sparse, real MusicBrainz release** — the original heuristic guessed
  "not yet fetched" from `track_count == 0 && status == ""`, which a real
  release with neither field populated could also match, causing an
  unbounded, repeated live re-fetch for that one release group on every
  scan and every version-dropdown open. Replaced with an explicit `fetched`
  column (migration 023) instead of guessing.
- **The album page's "Scan files" only ever looked in one specific CD
  subfolder of a multi-disc album**, not the album's other discs — it
  derived its scan directory from the first existing track file's own
  folder, and `ListTrackFilesByAlbum`'s path-ordering makes that
  deterministically the CD1 folder, so a new or changed file dropped into
  CD2 (or any other sibling disc folder) was never discovered. It now walks
  the common ancestor of every existing file's directory instead.
- **The unmatched-files auto-match panel could apply a stale pick's result
  after a newer one had already superseded it** — picking a different
  artist/album/version while a previous fetch was still in flight, or
  triggering a second auto-match before the first finished, could let the
  older async call's response clobber what the newer selection had already
  set. Every async chain (artist/album/version pick, auto-match) now
  carries a request token and discards its own result if a newer action
  has started since.
- **CD1/CD2/Disc-N folder-grouping for matching had several real gaps**,
  found and fixed across two passes the same night: a disc subfolder with
  no artist tag at all skipped the artist-agreement check entirely (one
  side blank, other populated read as "no conflict" instead of a mismatch,
  risking a merge into a completely different album/artist); a loose file
  sitting directly in the album's parent folder (e.g. a bonus track outside
  CD1/CD2) shared its group key with the merge target, so the catch-all
  copy-through-unmerged-groups step could silently overwrite the merged
  CD1+CD2 result with just that one loose file, discarding every disc
  track; and `discSuffixPattern` (both the Go scanner and its TypeScript
  mirror) only made a trailing `)` optional, never `]` (so `"Album [Disc
  2]"` wasn't stripped before comparison) and only stripped a *trailing*
  qualifier, never a *leading* one (`"CD1 - Moonglow"` didn't normalize to
  `"Moonglow"`). All fixed, with regression tests.
- **Removing an artist could leave a shared release group's cached cover
  art orphaned, or leave an owned album's cover art behind entirely.**
  `purgeArtistCaches` originally purged a removed artist's whole cached
  discography unconditionally, including a release group a *different*,
  still-present artist also references (a collaboration/split release both
  discographies list) — silent data loss for the artist left behind; fixed
  with a `ReleaseGroupMBIDsStillReferenced` check that skips anything still
  referenced elsewhere. Separately, owned albums' own cover art was never
  added to the purge list at all (only cached release-group *version*
  metadata was) — fixed by unconditionally including every owned album's
  release MBID before filtering.
- **An album that was Wanted before its files ended up matched via any path
  other than the grab→import pipeline kept showing up twice in the library
  grid** — once owned, once still wanted, each side resolving cover art
  differently (which is what made the latent bug visible as two
  different-quality covers for the same album). Fixed from both directions:
  `applyMatch` now clears any `wanted_albums` row for the same
  (artist, release group) once a file actually matches into it, and
  `GetOrCreateWantedAlbum` now refuses to create a wanted-album row for a
  release group that's already owned in the first place.
- **Artist biographies from TheAudioDB were always empty** — the client
  struct tagged the field `strBiographyEN`, but TheAudioDB's English
  biography field is actually just `strBiography` (only *other* languages
  get a suffix: `strBiographyDE`, `strBiographyFR`, …). Not a
  data-availability gap, a typo present since this client was written; the
  test fixtures used the same wrong field name, which is exactly why it
  went undetected — they validated the code against itself, not real
  TheAudioDB data. Confirmed against TheAudioDB's live API before fixing.
- **`backfillReleaseGroupVersions` (the sweep that catches artists added
  before version caching existed) was defined but never actually called
  from `handleTriggerMusicScan`** — the backfill never ran on any scan.
  Wired in, then moved to a background goroutine (fired after the scan's
  own `Running` flag clears) so it no longer blocks the scan-trigger
  request while it walks a whole library's discographies.
- **The library grid always showed "0 albums," even for artists that
  genuinely owned some** — `handleListMusicArtists` returned plain artist
  rows with no album-count field at all. Now attaches owned/total counts in
  bulk. The total was initially the artist's whole cached discography size
  (`CountReleaseGroupsByArtist`), which read as "1/126" for an artist with
  a big catalog and nothing else wanted — not useful — so it was changed
  again the same night to `owned/(owned+wanted)` via a new
  `CountWantedAlbumsByArtist`, matching what Missing actually offers.
- Also hardened `cacheAllVersionTracklists`/`getReleaseWithTracklist`
  against an unparseable cached tracklist row being treated as
  "already warm" forever, so a corrupted cache entry can self-heal on the
  next fetch instead of silently starving the tracklist view.
- **Importing a finished download copied everything in it, junk included** —
  NFOs, cover-art images, sample/proof folders, `.sfv`/`.m3u` sidecar
  files, all of it landed in the music library alongside the actual audio.
  Completed Download Handling now filters to audio-extension files only
  when copying a grab in; the rest is left in the download's own source
  path, which still gets deleted afterward same as before. A subdirectory
  holding nothing but junk is never even created at the destination.
- **The Activity page could take several real seconds to load, every
  time its 15-second queue cache expired** — measured directly: the
  configured SABnzbd-compatible debrid bridge's `history` endpoint takes
  3–5+ seconds per call even on an already-warm connection, and the queue
  endpoint blocked on a fresh live sweep of every client whenever its cache
  went stale. It now only blocks synchronously when there's no snapshot at
  all yet (a fresh server start); once any snapshot exists, a stale hit
  returns it immediately while a background sweep quietly refreshes it for
  next time. Verified live: first load after a restart ~5s (unavoidable —
  nothing to show yet), every load after that 20–40ms regardless of how
  stale the cache was.
- **Removing an artist or album with a download still in flight let that
  download finish and import anyway**, silently re-creating whatever had
  just been removed — `musicscanner` matches a file to an artist/album by
  the file's own tags/MBIDs regardless of what CantiNode's tables say, so a
  grab that outlived its now-deleted `wanted_albums` row (and, for an
  artist removal, the artist row itself) would still resolve to something
  on import. Removing an artist or album now cancels (fails) any grab still
  in flight for its wanted albums first.
- **A file — or its whole containing folder — deleted outside the app left
  a phantom `track_files` row behind forever**, undetected, on the album
  page's own **Scan files**. By design it never runs the whole-root-folder
  reconciliation a full library scan does (that would risk pruning a
  sibling album's rows from a directory-scoped walk), but that meant it had
  no way to notice a file was simply gone either. It now prunes exactly the
  album's own already-known files that a fresh `os.Stat` confirms are
  missing, still without touching anything outside that album.
- **A completed torrent grab could be silently mistaken for "vanished from
  the queue" and failed outright** — found while re-running the whole
  add → search → grab → import loop live to verify the fixes above.
  `download.magnetHash` stores a magnet's info hash lowercase, but a
  qBittorrent-compatible debrid bridge routinely echoes it back in
  whichever case the original magnet used — a case-sensitive string match
  between the two then never resolves, even though the download is
  perfectly healthy. `internal/importer`'s queue-to-grab matching, the
  Activity queue's own grab enrichment, and manually removing a queue item
  all did this exact comparison; all three now match case-insensitively,
  the same way `qbittorrent.go`'s own hash-lookup code already treated
  hashes internally. Also: removing a queue item manually resolved the
  grab as failed but never reverted its wanted album back to "wanted",
  leaving it stuck at "downloading" forever with no way to search again —
  it now reverts, matching what an automatically-detected failure already did.
- **A wanted album stayed listed as "downloaded" in the Wanted card
  forever** after a successful import, cluttering it with things no longer
  actionable — the real `albums` row created by the same import already
  represents ownership, and Missing already excludes anything with one, so
  the wanted_albums row is now deleted on successful import instead of
  marked done (mirroring the earlier "Remove/un-want" fix). The now-unused
  `downloaded` wanted-album status is gone.
- **"Search releases" was coming back empty against real indexers** —
  found immediately after the quality-profile scoring fix below actually
  started running. Real-world music release titles routinely name the
  *source* rather than the codec ("SHM-CD", "24-96 hdtracks", "4CD Box" —
  confirmed against a live Prowlarr search, where every single result
  omitted flac/mp3/m4a/opus/wav outright), but scoring treated an unstated
  format as an automatic rejection — so once scoring actually ran, it
  rejected nearly every real candidate. `PreferencesFor` now always allows
  an unstated format (scored below a release that does state a preferred
  one, never rejected for it).
- **"Ignore" on a wanted album could strand it forever, showing in neither
  Wanted nor Missing** — reported after a real album got lost this way.
  Ignoring set `wanted_albums.status = 'ignored'`, but
  `ListMissingArtistReleaseGroups` excludes a release group for as long as
  *any* `wanted_albums` row references it, regardless of status — so an
  ignored album never fell back into Missing, and the Wanted list filtered
  ignored rows out of its own display, so it vanished from both. The
  action is now a real removal (`DELETE /api/v1/music/wanted/{id}`,
  replacing `POST .../ignore`): it deletes the row outright instead of
  leaving one behind, which is what actually frees the release group back
  up for Missing. The `ignored` status is gone entirely. Also fixed a
  related bug this surfaced: wanting/un-wanting an album on the artist
  page only refreshed its own card, not the other one, so Missing and
  Wanted could show stale state relative to each other even before the
  strand-forever bug — both cards now refresh together.
- **Quality-profile scoring and the blocklist were never actually applied
  to a music search — found while auditing the repo for dead files.**
  `internal/release` (format preferences, size bounds, spam/executable
  rejection, dead-torrent rejection) had no caller anywhere in the
  codebase outside its own tests; `download.Store.BlockedKeys`/`IsBlocked`
  were the same. `handleSearchWantedMusicAlbum` was returning
  `indexer.Service.SearchAll`'s raw, unranked results straight to the
  caller — every quality-profile setting in `Settings → Quality Profiles`
  (and the whole "results ride the same scoring pipeline" claim made
  about the Prowlarr connection above) was cosmetic. Root cause: both were
  presumably wired in through `internal/autosearch`/`internal/importer`
  before those were deleted in the ebook/comic removal, and the
  music-specific search handler that replaced them was never updated to
  call either. Fixed: search results are now scored against the default
  quality profile and ranked, spam/dead-torrent/wrong-format releases are
  rejected, and anything already blocklisted is filtered out before the
  results ever reach the UI. New regression tests
  (`internal/api/music_test.go`) cover both.
- **The no-UI-embedded fallback page (`GET /` on a backend-only build)
  still said "The written-media automation server is running" behind the
  old LibriNode pen emoji** — missed by both the ebook/comic removal and
  the rebrand since it's a raw HTML string, not a doc or a `.tsx` file grep
  would catch. Now says "music" behind the music-note emoji, matching
  everywhere else.
- **Three of the four "background timing" settings did nothing.** Wanted
  search, metadata refresh, and import-poll cadences were still exposed in
  `Settings → General → Advanced` and `config.yaml` (`search_interval_hours`,
  `refresh_interval_hours`, `import_interval_seconds`), but their only
  consumers (`internal/autosearch`, `internal/refresh`, `internal/importer`)
  were deleted in the ebook/comic removal — the fields quietly saved and
  clamped without affecting anything. Removed, alongside the dead
  `import:` config section documenting a `Completed Download Handling`
  feature that no longer exists. Only the health-check cadence remains,
  since it's the only loop still on a schedule.
- **A series with a messy bibliography (duplicate rows, split-edition
  entries, or a stray title that's just the author's own name) could flag
  nearly every one of its releases as a "pack" and could make a real ebook
  fail to import at all, stuck waiting on a sibling that could never
  arrive.** Frank Herbert's Dune is the case that surfaced it: the author's
  bibliography carries entries like "Dune Messiah (1 of 2)" and "Dune
  Messiah (2 of 2)" alongside the real "Dune Messiah" — once title-matching
  strips their parenthetical suffix for comparison, all three reduce to the
  identical text, so every genuine "Dune Messiah" release looked like it
  also bundled two more books, and the importer's own file-matcher saw the
  same three-way tie and refused to pick one, treating an ordinary
  single-book download as unresolvably ambiguous. A book title that's
  nothing but the author's own name (stray bio/anthology metadata) had the
  same effect, since the author's name appears in virtually every release
  for them. Both the release-browser "pack" badge and the importer's
  title-based file matching now discount a candidate title once it reduces
  to text the wanted book's own title, or the author's name, already
  covers, and — when several candidates tie on the exact same matched
  text — prefer whichever one matched through its own primary title rather
  than through a fallback/stripped variant. Verified live: every Frank
  Herbert "Dune Messiah" release dropped from 119/119 falsely flagged as a
  pack to the one release that's genuinely a 3-book bundle, and a real
  "Children of Dune" release (whose bibliography carries the identical
  "(1 of 2)"/"(2 of 2)" duplicates) imported cleanly on the first try.
- **The same title-based "expected book count" fix below (audiobook packs
  waiting for a sibling folder the release's own title promises) now also
  covers ebook/manga/comic packs**, whose files can arrive one at a time in
  a shared download folder the same way audiobook folders do. Previously
  the ebook path only checked "does at least one file match the grabbed
  book" — a two-book release that had only synced its first file would
  import immediately rather than waiting for the second. It now also
  cross-references the release's own title against the author's
  bibliography and holds off until as many of those books have appeared as
  files as the title promises (manga/comic are unaffected — their volume
  count comes from series metadata, not a guessed title count). A lone
  file that matches no book by title at all is left alone (still imports
  immediately) so an oddly-named single-book download is never mistaken
  for a stalled pack. Verified live against a real 44-file torrent
  (`Poul Anderson collection sci-fi [epub]`, via TorBox): partway through
  the download only 2 of 5 newly-monitored target books had synced, and the
  final pass — once the rest arrived — correctly imported all 66 matched
  books in one go, including all 5 targets.
- **A pack could still silently drop a book even when nothing on disk was
  empty** — the deepest layer of the sync-delay issue above, and the one
  that needed live, repeated verification against a real TorBox-backed pack
  to actually confirm fixed. If one book's folder hadn't appeared *at all*
  yet while a sibling's had, the download looked, from the filesystem alone,
  exactly like an ordinary single-book release — there was nothing left to
  detect. The release's own title is the only independent signal available:
  it's now cross-referenced against the author's bibliography (the same
  signal behind the search browser's "pack" badge) to estimate how many
  books should eventually show up, and the import waits for that many
  folders — not just "more than one" — before settling for what's currently
  on disk. Also fixed: the "still syncing, retry" path never recorded why,
  so a manual "Import now" showed a bare skip count with no way to tell a
  sync delay from anything else; it now notes the reason, which is what
  made this last layer possible to pin down at all. Verified live: a
  two-book pack correctly held off on either book for ~15 seconds while its
  second folder was still absent, then imported both correctly in the same
  pass once it appeared.
- **A completed download whose files hadn't finished syncing to a network/
  debrid mount yet (client says done, share shows an empty folder) was
  immediately abandoned and blocklisted** instead of retried — a real bug,
  live-reproduced with a TorBox-backed torrent: the exact same release
  imported fine when given more time, but failed with "no audio files found
  in download" when an import pass ran seconds after the client reported it
  seeded, permanently discarding a perfectly good release. This is also what
  was actually behind "a pack imports only one of its books, seemingly at
  random" — whichever book's folder had synced by the time an import pass
  ran got kept; if none had, the whole release was blocklisted. An empty
  download folder is now retried like any other still-syncing download
  (same grace period before giving up as an unresolvable path); a folder
  that has real files, just none of the right kind, is unaffected and still
  fails immediately as spam/wrong content.
- **A pack whose books sync one folder at a time could silently drop
  whichever one wasn't ready yet, with no error and nothing to retry** — a
  narrower case the fix above didn't cover: the *whole* download wasn't
  empty (one book's folder already had its files), so it never hit the
  "completely empty, retry" path; it just looked like an ordinary
  single-book download and imported only the one that was ready. Now: a
  named subfolder that's itself still empty while a sibling isn't is
  treated as "not fully synced yet" for the whole download, not silently
  skipped — same retry-then-give-up handling as everywhere else. The same
  fix applies to the equivalent ebook/manga/comic case (the grabbed book's
  own file not yet appearing among an otherwise-present pack).
- **A pack whose grabbed book was already owned (and not an upgrade) skipped
  every other book in the pack too**, not just the one already owned. The
  early return that correctly skips placing an already-owned, non-upgrade
  primary book was also skipping the pack-extras step that fills the
  *other* books — the fix scopes that skip to the primary book only, so a
  pack still fills its other monitored/wanted books regardless of whether
  the book you grabbed needed a new file at all.
- **A multi-book pack could import only one of its books, inconsistently
  which one** — nothing previously stopped the periodic import sweep and a
  manual "Import now" click from overlapping. A pack's cleanup step
  (deleting the whole download folder once its books are copied out) racing
  against a second, still-in-progress pass mid-copy on that same download's
  *other* book would silently drop or corrupt whichever book that pass was
  working on — purely a timing accident, so sometimes the book you searched
  for came through and sometimes the other one did. Import passes are now
  serialized: a pass already running is waited out instead of raced.
- **Torrent downloads through a debrid bridge (TorBox) could never import,
  and never showed a status on their book's page either** — the bridge
  ignores CantiNode's rename request outright and always reports the
  uploader's own torrent name instead, which is routinely differently
  formatted from (or an outright typo of) the release title stored at grab
  time ("theq last emperox john scalzi" for "The Last Emperox"). Every
  matching path that fell back to comparing titles — import, and the queue's
  own grab-to-book linking that puts a "downloading" badge on a book's page —
  could never bridge that gap. A magnet already carries its own exact info
  hash, independent of any title or rename; it's now used directly as the
  grab's client item id, so matching no longer depends on the client
  reporting a title anywhere close to the one we grabbed under.
- **SABnzbd downloads never appeared in the Activity queue until they
  finished** — only completed usenet downloads showed up; anything still
  downloading was invisible. SABnzbd names a download's category field
  differently between its two endpoints — `cat` in the live queue, `category`
  in history — and the queue side was read with the wrong key, so every
  in-progress item failed CantiNode's own-downloads filter and was silently
  dropped. Only history (correctly keyed) ever showed anything, so a download
  only appeared once it was already done.
- **Torrent grabs could become permanently unmatchable at import — "cannot
  import files acquired via torrent"** — a regression from the fix just
  above. Giving every torrent grab a real id (by looking up its hash right
  after adding) could pick the wrong torrent's hash when a similarly-titled
  release was already in the client at grab time (an earlier series volume
  already seeding: "Dune" matches inside "Dune Messiah"), and a grab's title
  fallback was then skipped entirely because it now looked like it already
  had a "real" id. The hash lookup now compares against a snapshot taken
  right before adding, so an existing torrent can never be mistaken for the
  one just added; the title fallback also no longer requires an empty client
  item id, so a grab with a wrong one (including one already stuck that way
  from before this fix) is still found by title.
- **Removing a torrent download from Activity before it imported could leave
  that book permanently stuck reporting "a grab is already pending," blocking
  any new search or grab for it.** qBittorrent's add endpoint never echoes
  back the torrent's hash, so every torrent grab was recorded with an empty
  client item id; removing it from the queue could then only resolve (close
  out) the matching pending-grab record via an exact, punctuation-sensitive
  title match, and any mismatch (a tracker or the client itself mutating the
  name) silently left that grab open forever. The hash is now looked up right
  after adding — preferring an exact title match, falling back to a
  substring match — so removal (and the queue's own grab-to-book linking) can
  find the grab by its real id instead. This only prevents the problem going
  forward — a grab already stuck this way from before the fix stays stuck, so
  Activity → History now has a "cancel" button on any entry still reporting
  "grabbed": it manually clears CantiNode's own pending record without
  touching the download client, unblocking a new search or grab for that book.
- **Switching an author or book's metadata provider override and refreshing
  always failed with "not found at metadata provider"** — reproduced live with
  Frank Herbert pinned to Open Library after being added via Hardcover.
  Refresh was reusing the record's original provider's foreign id when calling
  the override provider, but that id belongs to a different provider's
  namespace entirely (Hardcover's author id means nothing to Open Library) and
  will never resolve there. Refresh now re-finds the record by name (authors)
  or title (books) on the override provider first, preferring an exact match.
  Fixing this also surfaced a second bug it would have hit next: once
  resolved, saving under the new provider's identity would have upserted by
  the (source, foreign id) natural key, which no longer matched the existing
  row — creating a duplicate author/book instead of updating it in place, and
  orphaning everything linked to the original. A refresh now always updates
  its known row by internal id, and an author-level provider switch matches
  its existing bibliography by title so owned/monitored books update in place
  too instead of duplicating alongside the fresh set.
- **Activity/queue could show another app's downloads.** A qBittorrent or
  SABnzbd instance shared with another *arr app (common with debrid-service
  bridges like TorBox) sometimes doesn't honor the category filter CantiNode
  already requests server-side, returning every app's items regardless. Both
  clients now also filter client-side by each item's own reported category, so
  another app's downloads never show up as CantiNode's.
- **An owned audiobook upgraded to a different-shaped format (e.g. a multi-file
  mp3 set upgraded by a single m4b) could end up "owned" with no file at all.**
  Multi-file audiobooks are recorded by their whole book folder; a single-file
  upgrade places its file one level inside that same folder. The old-file
  cleanup that runs right after placing the new one only skipped deletion when
  the old and new paths were identical — not when the new file was nested
  inside the old folder — so it deleted the whole folder, including the file
  it had just placed there. The library record survived, so the book silently
  looked owned with nothing on disk. Found via full UI QA testing; caught by a
  regression test that reproduces the exact scenario.
- **AudioBook Bay searches for ordinary book titles ("Dune Messiah", "The
  Hobbit") mostly failed with what looked like rate-limiting — that wasn't the
  real cause.** ABB's edge silently redirects any search whose query starts
  with an uppercase letter back to its homepage, and book titles are naturally
  Title Case, so nearly every real search hit it; the failure is
  indistinguishable from genuine throttling at the HTTP level (same redirect,
  same error message), which is why it read as intermittent rate-limiting
  rather than a deterministic bug. The query is now lowercased before it's
  searched. Bundled into the same pass: a blank listing or detail page (not
  just a homepage redirect) now gets the same one-retry treatment a redirect
  already had; the grab-time detail fetch gets a retry too, where before a
  single blip failed the whole grab; and the info-hash regex now tolerates
  whitespace and 64-char SHA256/v2 hashes, falling back to a raw magnet link
  when the Info Hash cell is missing.
- Release list: a torrent source with no swarm data (AudioBook Bay) now reads
  **"N/A"** for seeders/leechers instead of the same bare dash a genuinely dead
  torrent shows, and usenet and direct-protocol results — which have no such
  concept at all — no longer carry empty seeder/leecher placeholders.
- **An author's "missing" list drops foreign editions, box sets, and
  anthologies.** Hardcover catalogs every translation as its own book under the
  author, lists multi-author anthologies and magazine issues the author has one
  story in, and files graphic-novel adaptations under them too — so a
  bibliography was mostly not the author's own books (Frank Herbert ran to ~100
  entries, Andy Weir ~50). A book is now kept only when it looks like the
  author's own work: it has an edition in your metadata language (or enough
  readers to be a real work Hardcover just hasn't language-tagged), it isn't a
  known foreign edition, it isn't a box set (unless "Show box sets & collections"
  is on), and it credits a normal number of authors rather than the dozens an
  anthology does. Frank Herbert now lists his actual novels and stories instead
  of a dozen translated Dunes, *Nebula Winners Fifteen*, and *The Wesleyan
  Anthology of Science Fiction*. A metadata refresh also **reconciles** now — a
  bibliography entry the provider stops returning is removed, so the cleanup
  reaches libraries already full of the old junk; only books you never added to a
  library and own no file for are ever removed.
- **Hardcover search and author pages carry far less junk.** Hardcover lists many
  near-duplicate and ghost records for one work — a film study and two authorless
  records all titled "Dune" next to Frank Herbert's, plus reissues and
  translations promoted to their own book. Search now collapses true duplicates
  (same title + author) to the most-read record and drops same-title stragglers a
  dominant work dwarfs (under 1% of its readers) along with never-read ghost
  records; an author's bibliography drops repeated titles, keeping the canonical,
  most-read one. Genuinely distinct same-title works, each with real readers, both
  stay.
- **Open Library and Google Books work as selectable metadata sources** now, not
  just names in the list. Open Library's provider test searched for a stopword
  ("the"), which Open Library rejects with HTTP 422 — so testing or activating it
  failed with a Bad Gateway; it now queries a real word and validates cleanly
  (and identifies itself for Open Library's higher rate limit). Google Books'
  keyless access shares one global anonymous daily quota that is frequently spent
  already: a 429 with no key configured now says exactly that and points you to
  add a free API key, instead of a bare "HTTP 429". (Open Library needs no key.)
- **AudioBook Bay** stopped intermittently returning empty results or bouncing to
  the homepage on a long-running server: it opens a **fresh connection per
  request** instead of reusing the app-lifetime keep-alive pool, which the site
  throttles once one connection has served enough requests. A browser, curl, and
  a just-started process always worked against the same site and IP — only the
  server's reused pooled connection failed.
- **Library Genesis** searches keep the right book again: the author is read
  from the results table's own column (libgen.li stopped wrapping authors in
  links, so releases carried no author and the scorer rejected every one for
  "not mentioning the author"). And a downloaded ebook is saved by its **real
  type** — the direct client identifies the file from its content, so a book
  served from a `get.php` mirror URL is written as `.epub`, not the unusable
  `.php` the URL implied.
- The built-in **direct downloader** now validates what it receives: a mirror
  that answers with an error/landing page instead of the file (even one
  mislabeled as a download) is rejected instead of saved as a bogus "book", and
  a direct download is removed from the download folder once imported.
- A release **grabbed from the web UI now auto-imports**. The app ran two
  separate download services — one for the background importer, one for the API —
  and the built-in direct client's queue is in-memory, so a UI grab finished in a
  queue the importer never watched. The whole app now shares one download
  service, so a UI grab and the auto-importer act on the same queue. (Remote path
  mappings now apply to API-triggered imports too.)
- **AudioBook Bay** grabs resolve to a real magnet again: release URLs keep their
  trailing slash — the slash-less form returns an unfollowable 301, so no magnet
  was ever assembled. A search bounced to the homepage (ABB rate-limiting the IP,
  common on a shared/VPN exit IP) is now retried a few times with backoff,
  browser-style, and requests carry full browser navigation headers, before the
  rate-limit error is surfaced.
- Series links now reconcile on every metadata refresh: a book's series
  membership is set to exactly what the provider currently reports, so a stale
  or wrong link (e.g. a standalone the provider once mislabeled as part of a
  series) is dropped instead of sticking forever and corrupting the organized
  path via `{Series Title}`. Previously links were only ever added.
- **Scan** is now scoped like organize already was: scanning from a specific
  library (or author/series) page walks only that library's roots, not every
  root on the server.
- **AudioBook Bay** stopped hammering the site into IP bans: a search now makes
  a single listing request and defers each result's per-page magnet assembly to
  grab time (for the one release grabbed), riding a warmed-up browser-like
  session; a search bounced to the homepage surfaces as rate-limiting.
- An unmatched-file row whose path was long no longer collapses into a
  one-character-per-line vertical strip — the path takes its own line with the
  actions below.
- Performance: a scan used to write every file as its own autocommit SQLite
  transaction, so a library scan of a few thousand files took 30+ seconds
  (a no-op rescan barely faster). Ebook/audiobook/manga/comic scans now
  batch a whole root's writes into one transaction — 31.8s → 2.5s on a
  synthetic ~11,000-book/2,293-file library, 13.0s → 2.9s on a rescan.
  Magazines keep the old per-file behavior (materializing a new issue needs
  a second connection the batch would starve, given the database's
  single-connection cap) — the smallest-volume scan path in practice.
- `GET /book` unscoped was shipping every book of every media type to the
  browser just to populate the Ebooks/Audiobooks page's manual-match
  fallback list (5.7 MB of unused JSON at library scale). `GET
  /book?library=ebook|audiobook` filters server-side now.
- Log/token-leak sweep: Newznab/Torznab, SABnzbd, and ComicVine all carry
  their API key directly in the request URL's query string, so a connection
  failure (or an indexer error page echoing the query back) used to leak
  the raw key into the health banner, search-error notices, and the log
  file the in-app log viewer exposes. A new `internal/redact` package
  strips known credential-shaped query params — and scrubs their literal
  values out of response bodies too — from every error at the point it's
  raised, wired into the indexer client, both download clients, and
  ComicVine. Hardcover (header-based auth) and AniList (keyless) were never
  exposed to this. Regression tests assert the exact secret string never
  survives into the resulting error.
- Failure-mode polish across the four scenarios that used to degrade into a
  silent log line: a metadata provider that's down or unreachable now reads
  as a self-healing warning in the health banner, distinct from a genuinely
  rejected token/key (a new `ErrUnreachable` sentinel, wired through
  Hardcover/AniList/ComicVine); manga/comic series providers are health-
  checked too, scoped to libraries actually in use; a background refresh
  sweep aborts after three consecutive unreachable results instead of timing
  out on every remaining author/series one at a time; an indexer already
  resting in backoff is no longer re-probed by the health check (which would
  add load to something already known to be 429ing) and reports its
  resting-until time instead; and the importer's orphan sweep — which
  resolves a grab whose download vanished from its client — is now per-
  client, so one download client being briefly unreachable can't freeze
  orphan resolution for grabs sitting in a different, healthy client.
- The scan no longer silently attaches a file to a book that belongs only to
  the OTHER format library (the "added an ebook, it showed up in Audiobooks"
  linkage): the file lands in Unmatched with a confident suggestion, and the
  one-click import is the consent that enrolls the second format. Books in
  no format library yet still match freely on scan.
- A library page's Organize… now moves only that library's files (it used to
  organize every library at once); the rename API gained a mediaType scope.
- Manga/comic unmatched files without an auto-matched series can now be
  matched by hand: pick any series in the library, then one of its volumes.
- Verified with new regression tests that adding an author or book into one
  format library never enrolls the other (a title appearing in both means
  you own both formats — ownership enrolls by design).
- Scanner matches survive organizing: template-named files re-match their
  books on every scan (template-aware keys), manual matches stick, and a
  re-found file no longer duplicates its record.
- An import whose organized target already exists on disk but is unrecorded
  now adopts the file instead of skipping forever with the grab stuck.
- Format-less release names (manga/comic/magazine/audiobook convention) are
  scored instead of rejected; series/magazine title matching is whole-word so
  short titles no longer false-match longer ones.
- Failed and junk downloads have their data deleted directly on disk as well,
  covering clients that ignore the delete-files flag.

### Changed
- **Monitoring or refreshing an artist gets its own 5-minute timeout**
  (`handleMonitorMusicArtist`/`handleRefreshMusicArtist`), up from the 60s
  shared with every other single-request metadata endpoint — now that
  release-group pagination fetches every page instead of truncating (see
  Fixed above), an extremely prolific artist's discography sync can need
  more sequential MusicBrainz requests than the old budget comfortably
  allowed.
- Renamed the unmatched-files page's per-folder toggle from **"Auto-match…"**
  to **"Match…"** — it opens a panel for manual dropdown picking as much as
  automatic pre-filling, so "Auto-match" undersold what it actually does.
- Several per-row database round trips became single batched queries
  (`purgeArtistCaches`' version lookups, `DeleteReleaseGroupCache`'s
  per-mbid deletes, `ReplaceReleaseGroupVersions`' per-row insert,
  `SuggestMatches`' per-file lookup), and the unmatched-files page's
  "Approve all" now fires every suggestion's match request in parallel
  instead of awaiting them one at a time.
- **Prowlarr integration no longer pretends to be a Readarr application.**
  The old model added CantiNode to Prowlarr as an "application" — Prowlarr
  would push its indexers into CantiNode via a Readarr-v1-shaped API
  CantiNode had to emulate (fake metadata-profile/tag/schema endpoints,
  arr-shaped root-folder/quality-profile/download-client resources served
  only to Prowlarr's own User-Agent, a spoofed Readarr version number).
  All of that (`internal/api/prowlarr.go` and every `isProwlarr()` branch
  elsewhere) is gone. In its place: a new native indexer source,
  **Prowlarr**, selectable under Settings → Indexers like any other
  indexer type — point it at a Prowlarr instance's URL and API key, and
  CantiNode calls Prowlarr's own `GET /api/v1/search` directly (the same
  call Prowlarr's own search page makes), which fans out to every indexer
  Prowlarr has configured. One connection instead of duplicating each
  indexer in CantiNode's own settings, and no application-sync handshake.
  Results ride the exact same scoring/blocklist/grab pipeline as any other
  indexer's — Prowlarr's own search has no quality-profile concept, but
  going through CantiNode's now applies one. `internal/indexer.NativeDef`
  gained a `NeedsBaseURL` flag (Prowlarr has no sensible default URL,
  unlike a scraped site with a fixed domain) so the Settings form knows to
  require one.
- **Renamed LibriNode → CantiNode.** This codebase was rebuilt on top of a
  fork of [LibriNode](https://github.com/obstruct-exit-emit/LibriNode)
  (see the [roadmap](ROADMAP.md) for the fuller lineage); now that it's
  music-only, every "LibriNode" reference — the Go module
  (`github.com/librinode/librinode` → `github.com/cantinode/cantinode`),
  the binary (`cmd/librinode` → `cmd/cantinode`), the `LIBRINODE_*` env
  vars (→ `CANTINODE_*`), the default data directory (`%AppData%\LibriNode`
  / `~/.config/librinode` → `CantiNode` / `cantinode`), the database/log
  filenames, the systemd unit, the MusicBrainz User-Agent, and all UI/doc
  text — is renamed to CantiNode. The download-client `category` default
  moves from `librinode` to `cantinode` too; a new migration
  (`020_rebrand_cantinode.sql`) updates the column default and re-points
  any existing row still on the untouched default (a customized category
  is left alone). No other behavior changes.
- **Default port moved from `7845` (LibriNode's) to `7847`** — CantiNode's
  own convention alongside its sibling apps (LibriNode `7845`, AcerviNode
  `7846`). An explicit `port:` in `config.yaml` or `CANTINODE_PORT` still
  overrides it either way.
- **Library visibility is membership, not monitoring.** A prose title shows up
  in its Ebooks/Audiobooks library exactly when it's a member of that library;
  unmonitoring a book stops it from being auto-grabbed but no longer hides it
  from the grid. The owned-vs-total progress meter that implied "monitored =
  should own" is gone.
- Native sources still under real-world burn-in (AudioBook Bay, Library Genesis)
  are now flagged **WIP** in the indexer UI so their off-by-default, use-at-your-
  own-risk status is visible where you enable them.
- The metadata refresh sweep defaults to every **30 days** (was 24 hours) —
  metadata rarely changes and a monthly re-sync is kinder to providers.
  Per-item and manual refreshes are unaffected; tune it under Settings →
  General → Background timings (6–2160 hours).
- Magazines are organize-only for now: searching and downloading are disabled
  everywhere (wanted sweep, series search, release search, grab), while
  add-by-name, scanning, issue materialization, import, and organizing all
  keep working. The magazine search engine stays in the tree for later.
- Unstamped (dev) builds now report `dev-<sha> (<date>)` from the embedded
  build info instead of a stale placeholder; releases keep stamping real
  versions via ldflags.

### Removed
- **Ebooks, audiobooks, and comics are gone — CantiNode is now a music-only
  server.** Authors, books, editions, series, and every book-format library
  are removed, along with the metadata-provider subsystem that served them
  (Hardcover, ComicVine, Open Library, Google Books, AniList) and the
  importer/autosearch/organize/refresh pipeline built around them. Music
  (MusicBrainz + TheAudioDB, added over the prior series of changes) is now
  the sole library. Generic infrastructure Music depends on is untouched:
  root folders, quality profiles, indexers (Newznab/Torznab/native,
  including Prowlarr application sync), download clients, the grab
  queue/history/blocklist, health checks, backups, and auth/roles. Two
  functional notes: **Completed Download Handling and the automatic
  wanted-list sweep are gone with the importer/autosearch packages they
  lived in** — grabbed files are picked up by the next library scan
  instead of an automatic import pass, and search/grab are user-triggered
  only for now (tracked in the roadmap's Future section); and the
  `naming`/`opf`/`comicinfo`/`comiccover`/`scanner` packages are removed or
  trimmed to their still-generic pieces (`internal/relname` keeps the two
  release-name utilities release scoring and queue enrichment still use).
  Touches the DB schema (a new migration drops the book tables and rebuilds
  the shared `root_folders`/`quality_profiles`/`indexers`/`grabs` tables to
  match), the REST API, and the web UI (routing collapses to a single
  Music library).
- **Manga and magazines are gone as media types**, along with the
  colorized/monochrome per-variant root-folder system manga alone used.
  Comics remains as the one series-first library. Also removed: the
  **AudioBook Bay** and **Library Genesis** native indexers (scraped
  sources, off-by-default and user-enabled) — Prowlarr/Newznab/Torznab
  indexing is unaffected. Touches the DB schema (migrations trimmed to
  match), the scanner/organizer/release-scoring/importer pipeline, the
  REST API, and the web UI.
- **Docker and Windows support are on hold for now.** Both worked and shipped
  (a Dockerfile + compose file + published GHCR images; a Windows zip with a
  Task Scheduler install/uninstall script) but are pulled back mid-hardening
  to concentrate on a single well-supported Linux path through burn-in.
  Release CI now only builds Linux (amd64/arm64) tarballs + the systemd unit;
  the Docker publish job, the Windows build step, and CI's Windows test leg
  are all gone for now. Reintroducing them later is a matter of picking the
  work back up, not redesigning it — see the roadmap's Future section.

Earlier work (Phases 0–5) is chronicled in the README's roadmap section:
libraries for ebooks/audiobooks/comics, Hardcover/ComicVine metadata,
Prowlarr sync, quality profiles and upgrades, Completed Download Handling
with multi-book pack imports, per-library UI, health checks, authentication,
backups, packaging, and the docs site.

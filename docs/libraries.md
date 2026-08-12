# Libraries

Music only appears in the sidebar once you add a root folder for it
(Settings → Media Management) — content alone never surfaces it, Plex-style.
The library page is a poster grid of artists with owned/total album counts.
Grids over 10 cards get a filter box and render incrementally.

## Artists (artist-first, two levels deep)

Browsing: library grid (artists) → **artist page** → **album**.

The artist page has a photo, bio, a **monitored/unmonitored** toggle, and
artist-scoped actions (**Refresh metadata**, **Scan files** — see the note
on scan scope below, **Organize…**, **Remove artist**) — each touches only
this artist. Below that: one **Albums** grid (Grid/Compact/List views,
sortable by release date or title) holding both owned and wanted albums
together, badged **owned**/**wanted**/**downloading**, and a **Missing**
section for the rest of the discography. Cover art shows for wanted and
missing albums too, not just owned ones — resolved via each release
group's cached representative release (see
[Release versions](#release-versions) below), fetched and cached the first
time it's shown.

Adding an artist pulls their discography as metadata only — nothing is
auto-monitored or auto-wanted, so a freshly added artist's whole discography
starts in **Missing**; an artist with zero owned or wanted albums still
shows, with an empty grid pointing at Missing, instead of disappearing.
**Missing** groups the gaps by release type (Album/EP/Single/Live/
Compilation/…); each row has **+ Add** (want it without touching the
artist's monitored state) and **+ Add & Monitor** (want it and monitor the
artist) — neither one searches or grabs anything by itself, they just mark
the album wanted and it moves into the Albums grid. Clicking a wanted
album's card there opens **Search releases** (scored candidates to
hand-pick a **Grab** from) and **Stop wanting**. A **monitored** artist's
wanted albums are additionally swept and grabbed automatically on a
schedule (see [Acquisition](acquisition.md)) — monitoring is what opts an
artist into that, independent of which specific albums are wanted.

An album's own page shows its cover and tracklist, each track's matched
file(s) with per-file **organize**, **write tags**, and **delete** actions,
plus its own album-scoped actions: **Scan files** and **Organize…** (both
genuinely scoped to just this album's own folder — see below), **Remove
album**, and (once **upgrades allowed** is on for the profile and this
album's format hasn't already met its cutoff) **Search upgrade** to look
for and grab a better release than what's currently owned. There's no
monitor toggle here — wanting/monitoring both live at the artist level.

Refreshing metadata never enrolls, un-enrolls, or re-monitors anything —
only bio/photo/cover-art/new-release metadata update.

**Remove artist** deletes everything cached for that artist, not just the
library rows — its release-version list, cached tracklists, cover art, and
photo are all purged too, since the artist is no longer in the library at
all. **Remove album** (the album's own page) is narrower on purpose: it
only removes that one album's ownership record, leaving the artist's
cached discography metadata (including that same album's cached versions
and cover art) alone, since the artist and the rest of its discography are
still here.

## Existing-file import (unmatched files)

Scanning matches files in layers. First by **tag**: a file's own embedded
`MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID` tags place it outright,
however oddly it's named. Then by **whole-folder release matching**
(track count and titles against a MusicBrainz release) — CD1/CD2/Disc-N
sibling subfolders of the same multi-disc album are detected and merged
into one matching group first (purely for matching purposes; files stay
exactly where they are on disk until an explicit Organize), even when each
disc's own Album tag carries a different per-disc suffix ("Album CD 1" vs
"Album CD 2"). A folder bundling genuinely different albums together (a
discography/box-set dump) is left ungrouped instead — grouping only kicks
in when the discs actually agree on the same album. Then by **fuzzy title
search** (typo- and word-order-tolerant) for anything left. Files that
still can't be confidently placed land in an **Unmatched** list for manual
review. A newly-discovered artist (one your files matched to, that you
hadn't explicitly monitored) gets its metadata cached automatically too.

**Unmatched Files** (sidebar, under Libraries) is the review queue for
whatever a scan couldn't place, grouped by folder — a merged CD1/CD2 pair
reviews as one group, the same as automatic scanning treats it. Each group
can be matched two ways:

- **Per file, by hand** — a search form pre-filled from the file's own
  tags; pick a MusicBrainz recording and it's matched.
- **Auto-match, for the whole group** — cascading Artist → Album → Version
  dropdowns, each narrowed by the field before it, scoped to your own
  library's wanted/missing albums rather than a fresh MusicBrainz search.
  The **Auto-match** button pre-fills all three itself when confident
  enough: artist and album by name match against the folder's own tags
  (threshold tunable — see
  [Configuration](configuration.md#music-matching)), version by comparing
  the folder's own file count against each cached edition's track count.
  Nothing is proposed until **Suggest matches** is clicked, and nothing is
  applied until each suggestion is individually **Approve**d (or
  **Approve all**) — every field stays yours to review and change first,
  whether it was auto-filled or picked by hand.

## Release versions

A release group ("the album" — MusicBrainz's stable identity for it)
usually has several actual releases behind it: different pressings,
editions, remasters, formats. The Version dropdown above lets you match
against a specific one instead of always falling back to one fixed default
release. Every known version's metadata (title, date, country, disc/format
layout, track count) and full tracklist gets cached the first time an
artist's discography is synced — monitoring, an explicit refresh, or the
backfill sweep that runs on the next scan for an artist added before this
existed — so picking among them afterward never calls MusicBrainz again.

## Scanning & organizing

**Scan files** on the Music page or an artist's own page walks every music
root folder — both trigger the same library-wide scan; there's no
artist-scoped scan. An **album's own page** is the exception: its **Scan
files** button really is scoped to just that one album, walking only its
own folder (derived from where its existing files already live) and never
touching a sibling album's records — useful for picking up a file you
dropped in by hand without paying for a full library walk. **Organize…**
is scoped everywhere it appears: from an artist page it previews and
applies moves for that artist's own files, from an album page for just that
album's.

**Organize…** previews, then applies, moves that bring files in line with
the naming template (**Settings → Media Management**). Emptied folders are
swept up to (never including) the root; a move that would overwrite an
existing file is refused rather than silently clobbering it.

Non-audio files (download junk like `.nfo`/cover art/`.sfv`) never enter the
library in the first place — Completed Download Handling only copies audio
files out of a finished download (see [Acquisition](acquisition.md)), and
a manual scan only ever looks at audio files too — so there's nothing for
Organize to clean up after the fact.

## Activity

**Activity** shows the live download queue (with per-item progress) and
grab history across the whole server — see
[Acquisition](acquisition.md#download-clients) for the queue/blocklist/
history details.

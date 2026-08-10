# Libraries

Music only appears in the sidebar once you add a root folder for it
(Settings → Media Management) — content alone never surfaces it, Plex-style.
The library page is a poster grid of artists with owned/total album counts.
Grids over 10 cards get a filter box and render incrementally.

## Artists (artist-first, two levels deep)

Browsing: library grid (artists) → **artist page** → **album**.

The artist page has a photo, bio, a **monitored/unmonitored** toggle, and
artist-scoped actions (**Refresh metadata**, **Scan files**, **Organize…**,
**Remove artist**) — each touches only this artist. Below that: an
**Albums** grid (Grid/Compact/List views, sortable by release date or
title), a **Wanted** section, and a **Missing** section.

Adding an artist pulls their discography as metadata only — nothing is
auto-monitored or auto-wanted, so a freshly added artist's whole discography
starts in **Missing**; an artist with zero owned albums still shows, with an
empty grid pointing at Missing, instead of disappearing. **Missing** groups
the gaps by release type (Album/EP/Single/Live/Compilation/…); each row has
**+ Add** (want it without touching the artist's monitored state) and
**+ Add & Monitor** (want it and monitor the artist) — neither one searches
or grabs anything by itself, they just mark the album wanted. **Wanted**
lists everything you've marked that way, with per-item **Search releases**
(lists scored candidates to hand-pick a **Grab** from) and **Ignore**.

An album's own page shows its cover and tracklist, each track's matched
file(s) with per-file **organize**, **write tags**, and **delete** actions
— there's no monitor toggle or grab controls here; wanting and grabbing an
album both happen from the artist page.

Refreshing metadata never enrolls, un-enrolls, or re-monitors anything —
only bio/photo/cover-art/new-release metadata update.

## Existing-file import (unmatched files)

Scanning matches files in layers. First by **tag**: a file's own embedded
`MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID` tags place it outright,
however oddly it's named. Then by **whole-folder release matching**
(track count and titles against a MusicBrainz release). Then by **fuzzy
title search** (typo- and word-order-tolerant) for anything left. Files
that still can't be confidently placed land in an **Unmatched** list for
manual review. A newly-discovered artist (one your files matched to, that
you hadn't explicitly monitored) gets its metadata cached automatically
too.

## Scanning & organizing

**Scan files** always walks every music root folder — the button appears
both on the Music page and on each artist page (for convenience, so you
don't have to navigate back), but either one triggers the same library-wide
scan; there's no per-artist scan yet. **Organize…**, by contrast, *is*
scoped to wherever you click it: from an artist page, it only previews and
applies moves for that artist's own files.

**Organize…** previews, then applies, moves that bring files in line with
the naming template (**Settings → Media Management**). Emptied folders are
swept up to (never including) the root.

Organize **scans first** so the plan always reflects what's actually on
disk — no separate Scan click needed. The preview also includes a
**cleanup**: files that don't belong (download junk like `.nfo`/`.torrent`)
listed with a checkbox to delete them and prune every empty folder on
apply. Matched files, unmatched media (the import flow's domain), and
artwork images are always kept, and every deletion is re-validated
server-side against the library's own roots — nothing outside them is ever
touched.

## Activity

**Activity** shows the live download queue (with per-item progress) and
grab history across the whole server — see
[Acquisition](acquisition.md#download-clients) for the queue/blocklist/
history details.

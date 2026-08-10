# Libraries

Music only appears in the sidebar once you add a root folder for it
(Settings → Media Management) — content alone never surfaces it, Plex-style.
The library page is a poster grid of artists with owned/total album counts.
Grids over 10 cards get a filter box and render incrementally.

## Artists (artist-first, two levels deep)

Browsing: library grid (artists) → **artist page** → **album**.

The artist page has a photo, bio, and artist-scoped actions (**Search
wanted**, **Organize…**, **Scan files**, **Refresh metadata**, **Remove**),
a grid of every album they have (grouped by type: Album/EP/Single/
Live/Compilation/…), and a **Missing** section below it: the rest of the
discography, each row expandable with a one-click **+ Monitor** that starts
searching. Adding an artist pulls their discography as metadata only —
nothing is auto-monitored, so a freshly added artist's whole discography
starts in Missing; an artist with zero owned albums still shows, with an
empty grid pointing at Missing, instead of disappearing.

An album has cover art, the monitor toggle, **Auto grab**/**Search
releases**, and remove (with an opt-in delete-files checkbox).

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

## Scanning & organizing (scoped per library)

**Scan files** and **Organize…** always act on **only the music root(s)**
— an artist page scans/organizes just that artist's files.

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

## Wanted and Activity

The artist page has a **Wanted** card (monitored but missing albums, with
per-item search and live download progress). **Activity** shows the
download queue and grab history across the whole server.

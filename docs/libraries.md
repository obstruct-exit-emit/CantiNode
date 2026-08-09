# Libraries

A library only appears in the sidebar once you create it by adding a root
folder for its media type (Settings → Media Management) — content alone
never surfaces one, Plex-style. Library pages are poster grids (authors for
prose, series for the rest) with owned/total counts. Grids over 10 cards get
a filter box and render incrementally.

## Ebooks & Audiobooks (author-first, three levels deep)

Prose books flow from Hardcover. **Ebooks and Audiobooks are separate
libraries with explicit membership** — for both authors and books:

- An **author** appears in Audiobooks only if added there or you own an
  audiobook of theirs (and vice versa for ebooks); adding/removing an
  author in one format never touches the other.
- A **book** appears in a format library only if you own that format or
  deliberately added it there — never inferred. **Membership decides
  visibility:** every enrolled book shows in the Books grid, monitored or
  not, owned or not. The per-book monitored flag only controls automatic
  grabbing and upgrading — it never hides a book. The **Missing** section is
  the complement: bibliography books you haven't added to this library yet.

Browsing: library grid (authors) → **author page** → **book page**.

The author page has a portrait, bio, and author-scoped actions (**Search
wanted**, **Organize…**, **Scan files**, **Refresh metadata**, **Remove
from Ebooks/Audiobooks** — all touch only this author's books in this
library), a poster grid of every book they have enrolled here (monitored or
not), and a **Missing** section below it:
the rest of the bibliography, grouped by series (then standalones by year),
each row expandable to a thumbnail + blurb with a one-click **+ Monitor**
that enrolls the book and starts searching. Adding an author pulls their
bibliography as metadata only — the canonical works, ordered by Hardcover
readership, so prolific authors get their actual canon rather than a random
slice of translations and reprints — and nothing is auto-monitored, so a
freshly added author's whole bibliography starts in Missing; an author with
zero visible books still shows, with an empty grid pointing at Missing,
instead of disappearing.

The book page has cover art, description, the monitor toggle, **Auto
grab**/**Search releases**, remove-from-library (with an opt-in
delete-files checkbox), and cross-add to the other format — once a book is
in both, this switches to a status badge instead of a button.

- Add from the Ebooks page → the author/book joins the Ebooks library.
- Cross-add from the book page (**+ Add to Audiobooks/Ebooks**, with a
  monitor prompt).
- Scanning/importing a format's file auto-enrolls the book (and its
  author) there — with one deliberate exception: a scan never *silently*
  attaches a file to a book that belongs only to the **other** format
  library. That file lands in Unmatched with a confident suggestion, and
  the one-click import is the consent that enrolls the second format —
  adding an ebook can't quietly grow an audiobook presence. (A book in no
  format library yet matches freely; its first owned file decides its
  first home.)
- Refreshing metadata never enrolls, un-enrolls, or re-monitors anything —
  only descriptions/covers/new-book metadata update.

Audiobook scanning understands `Author/Title.m4b` and multi-file
`Author/Title/*.mp3` layouts; imports land as `Author/Book Title/` folders
with a `metadata.opf` sidecar — Audiobookshelf-ready. Ebooks get a
`<file>.opf` sidecar for Calibre.

## Manga & Comics (series-first)

Search the provider, add the series, and every volume/issue appears on its
page with owned/wanted badges. Manga metadata comes from **AniList** (no
key) or **Hardcover** (reuses your Hardcover token); comic metadata from
**Hardcover** (the default) or **ComicVine** (free key) — choose each
provider under **Settings → Metadata** — or **None** to disable the
library's metadata entirely (libraries always honor the settings: under
None nothing is fetched, not even on refresh). Switching
a provider re-sources existing series on their next refresh: each
series is re-matched by title on the newly selected provider, re-bound in
place (monitoring and owned files kept — owned volumes hand their files to
the same-numbered new volume), and its volumes re-synced from the new
provider. Every author and series page also carries a **provider
override** (off by default): pin a record to a provider and its refreshes
use that one, beating the global selection — including None. That's how a
mixed library stays stable: pin the exceptions, let everything else follow
the settings. Like adding an author, **adding a series pulls metadata only**:
every volume/issue starts unmonitored in the series' Missing section (and a
fresh magazine doesn't auto-grab) until you monitor items selectively or
flip the series' monitor toggle — which monitors every volume at once and
doubles as "monitor future volumes", so refreshes (manual, or the periodic
sweep — every 30 days by default, tunable under Settings → General) monitor
newly discovered ones too. Imports write
`ComicInfo.xml` into CBZ archives and use Kavita/Komga-friendly
`Series/Series Vol. N.cbz` layouts.

Provider quirks: ongoing manga often have no official volume count on
AniList yet — they add with zero volumes and fill in as AniList publishes
totals — and AniList synthesizes volumes without per-volume descriptions
(left blank rather than repeating the series blurb on every volume).
Hardcover carries real per-volume descriptions and covers: volumes are
numbered by the series' positions, position-0 spin-offs are dropped, and
each volume keeps one edition chosen by the global metadata preferences
(**Settings → Metadata → Preferences**) — the edition matching your
language, then the standard (non-reissue/box-set) printing, then your
country, then the richest description; sequential numbering is only a
fallback for series with no positions at all. The preferences are
provider-agnostic: any provider that carries the data honors them
(AniList also uses the language to pick English vs romaji titles, and its
adult-flagged series stay out of search unless "include adult content"
is on).

Manga and comic series get the full author/book treatment. The series page
carries series-scoped **Search wanted**, **Organize…**, **Scan files**, and
**Refresh** (each touches only this series). The volume/issue list stays
compact — title + owned/wanted badge — and every row expands to a cover,
blurb, file locations, and the same controls an individual book has: a
monitor toggle, **Auto grab**, **Search releases**, and **Remove from
library** (opt-in delete-files). Volume/issue covers default to the
provider's art — **Settings → Metadata** has a per-library toggle to switch
manga or comics to extraction from the owned archive's first page (CBZ or
CBR, the latter read via pure-Go rardecode) instead, and extraction always
falls back to the provider's art when it yields nothing.
A per-series **Missing** section lists the volumes/issues you're not
tracking — neither monitored nor owned — each with a one-click **Monitor**;
removing one forgets its file records so it drops into Missing, and the
next scan re-finds any files left on disk.

### Colorized & monochrome variants

Manga can be owned in both a colorized and a monochrome edition without
splitting the library. Add a **separate root folder per variant** — a
monochrome/colorized selector appears when the media type is manga
(monochrome is the default, and pre-existing manga roots are treated as
monochrome). Files scanned or imported under a root inherit its variant.

A volume is one metadata row that tracks each variant independently. The
volume list stays compact for long series — each row is the title and a
single owned/wanted badge — and an owned volume expands to show which
variants it owns (`🎨 colorized` / `◻️ monochrome`) and where each file
lives on disk. Grabbing is variant-agnostic (a release doesn't reveal
whether it's color or mono); per-variant ownership is recorded by the
scanner as files land under their variant root.

## Magazines (provider-less, organize-only)

Add a magazine **by name**; LibriNode recognizes issues by date or number in
file names (`The Economist - 2026-07-04.pdf`, `Issue 452`). Scanning
materializes owned issues automatically, the existing-file import (below)
adopts fuzzy-named strays, and organized issues land as
`Magazine/Magazine - date.pdf`.

**The magazine library is organize-only for now**: searching and downloading
are disabled everywhere — the wanted sweep skips magazines, the series
Search-wanted button is hidden, and the release/grab endpoints reject
`mediaType=magazine`. Everything organizational (add by name, scan, import,
organize, browse, calendar) still works.

## Existing-file import (unmatched files)

Scanning matches files in layers. First by **identifier**: an **ISBN** (read
from the filename or an epub's embedded metadata) or Amazon **ASIN** matched
against a known edition places a file outright, however oddly it's named
(ISBN-10 and ISBN-13 are treated as the same book, and every candidate is
checksum-validated so a stray number can't pose as one). Then by **title**, the
exact author/title matching as before. Anything left goes to the Unmatched card
below — where a **fuzzy** pass (typo- and word-order-tolerant) pre-fills the
import picker with the closest book when it's confident enough, as a suggestion
you confirm, never an automatic import.

Every library page ends with an **Unmatched files** card when a scan found
files it couldn't confidently place (an ISBN-matched file is placed during the
scan and never lands here). Each row shows the library's best suggestion with a
**0–100% confidence rating** (100% = exact title; a unique longer match scores
by how much of the filename it explains and its lead over the runner-up; ties
cap at 40% and never auto-import; a fuzzy guess is offered pre-selected but
never auto-imported):

- **Confident rows import in one click** — and **Import all matched (N)**
  takes every confident row at once. Adopted prose books are enrolled in the
  library and monitored, like books added by hand.
- **Duplicates** (the file matches a book/volume/issue already owned) show
  both files side by side with **Replace** (this file takes the library
  copy's place — the old file is deleted from disk) or **Delete** (this file
  is deleted, the library copy kept). Manga is variant-aware: a colorized
  file only duplicates — and Replace only touches — the colorized copy.
- **Unknown owners get a one-click add**: an unrecognized author folder
  offers "+ Add ‹author›" (provider search inline), an unknown manga/comic
  series offers "+ Add ‹series›", and an unknown magazine is created by
  name on the spot. After adding, the row (and its siblings) gain real
  suggestions.
- The manual fallback lists the author's books (prose) or the series'
  unowned volumes; **dismiss** forgets the record without touching disk.

Per library: prose matches the author folder against the author's
bibliography; manga/comics parse the series (folder or filename prefix,
fuzzy-tolerant) and the `v02`/`#07` volume number; magazines parse the title
and issue date/number — a confident magazine import materializes the issue
on the spot.

## Scanning & organizing (scoped per library)

**Scan files** and **Organize…** always act on **only the library you're in**
— scanning from the Manga page walks manga roots, not every root on the
server; organizing from Ebooks moves ebook files only. Both narrow further on
the pages that have them: an author page scans/organizes just that author's
format library, and a series page just that series.

**Organize…** previews, then applies, moves that bring files in line with the
naming templates (**Settings → Media Management**) — all five media types,
multi-file audiobooks moving as whole folders with their sidecars. Emptied
folders are swept up to (never including) the root.

Organize **scans first** (scoped to the same level) so the plan always
reflects what's actually on disk — no separate Scan click needed. On a
library page, the preview also includes a **cleanup**: files that don't
belong in the library — download junk (`.nfo`, `.torrent`) or another
type's media dumped in this root — listed with a checkbox to delete them
and prune every empty folder on apply. Matched files, unmatched media (the
import flow's domain), `.opf` sidecars, artwork images, and `ComicInfo.xml`
are always kept, and every deletion is re-validated server-side against the
library's own roots — nothing outside them is ever touched.

## Wanted, Home, and Calendar

Every acquiring library page has a **Wanted** card (monitored but missing
that format's file, with per-item search and live download progress) —
magazines, being organize-only, have none. **Home** shows per-library
Recently-added and Wanted rows — types never mix, and the magazine section
carries no Wanted row. **Calendar** lists dated releases across all
libraries, upcoming and recent.

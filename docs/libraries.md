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

## Comics (series-first)

Search the provider, add the series, and every issue appears on its page
with owned/wanted badges. Comic metadata comes from **Hardcover** (the
default, reuses your Hardcover token) or **ComicVine** (free key) — choose
the provider under **Settings → Metadata** — or **None** to disable the
library's metadata entirely (libraries always honor the settings: under
None nothing is fetched, not even on refresh). Switching
provider re-sources existing series on their next refresh: each
series is re-matched by title on the newly selected provider, re-bound in
place (monitoring and owned files kept — owned issues hand their files to
the same-numbered new issue), and its issues re-synced from the new
provider. Every author and series page also carries a **provider
override** (off by default): pin a record to a provider and its refreshes
use that one, beating the global selection — including None. That's how a
mixed library stays stable: pin the exceptions, let everything else follow
the settings. Like adding an author, **adding a series pulls metadata only**:
every issue starts unmonitored in the series' Missing section until you
monitor items selectively or flip the series' monitor toggle — which
monitors every issue at once and doubles as "monitor future issues", so
refreshes (manual, or the periodic sweep — every 30 days by default,
tunable under Settings → General) monitor newly discovered ones too.
Imports write `ComicInfo.xml` into CBZ archives and use Kavita/Komga-friendly
`Series/Series Vol. N.cbz` layouts.

Provider quirks: Hardcover carries real per-issue descriptions and covers:
issues are numbered by the series' positions, position-0 spin-offs are
dropped, and each issue keeps one edition chosen by the global metadata
preferences (**Settings → Metadata → Preferences**) — the edition matching
your language, then the standard (non-reissue/box-set) printing, then your
country, then the richest description; sequential numbering is only a
fallback for series with no positions at all. The preferences are
provider-agnostic: any provider that carries the data honors them.

Comic series get the full author/book treatment. The series page carries
series-scoped **Search wanted**, **Organize…**, **Scan files**, and
**Refresh** (each touches only this series). The issue list stays
compact — title + owned/wanted badge — and every row expands to a cover,
blurb, file locations, and the same controls an individual book has: a
monitor toggle, **Auto grab**, **Search releases**, and **Remove from
library** (opt-in delete-files). Issue covers default to the provider's
art — **Settings → Metadata** has a per-library toggle to switch to
extraction from the owned archive's first page (CBZ or CBR, the latter
read via pure-Go rardecode) instead, and extraction always falls back to
the provider's art when it yields nothing.
A per-series **Missing** section lists the issues you're not tracking —
neither monitored nor owned — each with a one-click **Monitor**; removing
one forgets its file records so it drops into Missing, and the next scan
re-finds any files left on disk.

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
  is deleted, the library copy kept).
- **Unknown owners get a one-click add**: an unrecognized author folder
  offers "+ Add ‹author›" (provider search inline), and an unknown comic
  series offers "+ Add ‹series›". After adding, the row (and its siblings)
  gain real suggestions.
- The manual fallback lists the author's books (prose) or the series'
  unowned volumes; **dismiss** forgets the record without touching disk.

Per library: prose matches the author folder against the author's
bibliography; comics parse the series (folder or filename prefix,
fuzzy-tolerant) and the `v02`/`#07` volume number.

## Scanning & organizing (scoped per library)

**Scan files** and **Organize…** always act on **only the library you're in**
— scanning from the Comics page walks comic roots, not every root on the
server; organizing from Ebooks moves ebook files only. Both narrow further on
the pages that have them: an author page scans/organizes just that author's
format library, and a series page just that series.

**Organize…** previews, then applies, moves that bring files in line with the
naming templates (**Settings → Media Management**) — every media type,
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

Every library page has a **Wanted** card (monitored but missing that
format's file, with per-item search and live download progress). **Home**
shows per-library Recently-added and Wanted rows — types never mix.
**Calendar** lists dated releases across all libraries, upcoming and
recent.

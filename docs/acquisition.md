# Acquisition

Acquisition serves **ebooks, audiobooks, and comics**.

## Indexers

Two ways in:

- **Manually** under **Settings → Indexers**: any Newznab (usenet) or
  Torznab (torrent) endpoint, including per-indexer feed URLs from Prowlarr
  or Jackett. Test buttons on the form and on every saved indexer.
- **Prowlarr sync**: in Prowlarr, add an application of type **Readarr**
  with LibriNode's URL and API key. Prowlarr pushes its indexers into
  LibriNode and keeps them in sync (LibriNode emulates the Readarr v1 API).

Each indexer carries per-type category lists: books `7000,7020`, audio
`3030`, comics `7030` — adjust per indexer if yours differ.

An indexer that keeps failing **rests with exponential backoff** (5 minutes
doubling up to 6 hours) instead of being retried every sweep; one success
clears it.

### Native indexers

Some sites speak no Newznab/Torznab API, so Prowlarr structurally can't reach
them. A **native** indexer is a built-in source, selected as the indexer's
*type* under **Settings → Indexers** (no URL to paste) — it feeds the same
search, scoring, and grab pipeline as everything else. Native indexers are
LibriNode-managed only and are hidden from Prowlarr, so it never treats them as
indexers it owns. No native sources ship built in today; the framework stays
in the tree for a future source to register against.

### The `direct` protocol

Some sources hand out plain HTTP links, not torrents or NZBs, so LibriNode has
its own **direct** download client — add it under **Settings → Download
Clients** with a local download folder as its "host". It streams the file
itself, **failing over across a `|`-separated mirror list**, following a
membership-API JSON answer or an open-mirror landing page one hop to the real
file, and Completed Download Handling imports the result like any other grab.
The saved file is named by its **actual content**, so a link that resolves to
a `get.php`-style URL is still written with the right extension (not the
`.php` the URL implies); a mirror that answers with an error or landing page
instead of the file is rejected rather than saved as a bogus book; and because
the file is streamed only to be imported, it's removed from the download
folder afterward. It's source-agnostic — any native indexer that resolves to
plain HTTP links can ride it — but with no native sources shipping today,
there's currently nothing that grabs through it.

## Release scoring & quality profiles

Search results are parsed (author/title/year, formats, retail, language,
narrator/bitrate/abridged for audio, volume numbers, issue dates) and scored
against the **default quality profile** for the media type (**Settings →
Quality Profiles**): ordered format preferences, language, size bounds, retail
bonus. Candidates that can't be the book you asked for are rejected outright.

Comic and audiobook release names often omit the file format — a scan is
just `Vol. 01 (Digital)`, an audiobook carries the bitrate or narrator
instead of `m4b`/`mp3`. Those are accepted (a named format still ranks
higher) and the real format is read from the downloaded files at import;
ebooks still require a recognized format in the name.

With **upgrades allowed**, owning a lesser format keeps the book wanted
until the profile's cutoff; upgrade grabs must be strictly better, and the
import replaces the old file.

## Download clients

qBittorrent (torrents) and SABnzbd (usenet), under **Settings → Download
Clients** — category-scoped so LibriNode only ever touches its own
downloads.

LibriNode resolves each release on **its own side** before handing it off:
it fetches the NZB and uploads the file (SABnzbd `addfile`), and follows a
torrent to its magnet or downloads the `.torrent` and uploads the bytes. So a
download client behind NAT — or a SABnzbd/qBittorrent-compatible **debrid
bridge** (Real-Debrid, TorBox) whose cloud side can't reach your LAN
indexers — still works. Adds to a slow debrid bridge are confirmed against the
client's list so a slow response never loses the grab. A torrent grab is
tracked by the **magnet's own info hash**, not by name: some bridges (TorBox
included) ignore LibriNode's rename request and always report the
uploader's own torrent name instead — sometimes wildly different from, or a
typo of, the release title — so tracking by hash keeps import and the
queue's book linking working regardless of what name the bridge shows.

- **Automatic search** sweeps all wanted items every 6 hours; **Search
  wanted** and per-item **Auto grab** run on demand; **Search releases**
  lists scored candidates for hand-picking.
- **Completed Download Handling** (every minute): finished downloads are
  imported into the naming-template layout and grab history updated. Three
  **Import handling** options (Settings → Download Clients), **all on by
  default**, govern the rest: *import whole packs*, *remove the completed
  download from the client*, and *delete the downloaded files*. Turn the last
  two off to leave torrents seeding and the originals in place — usenet
  history is cleared either way, since LibriNode only ever copies from it.
- **Multi-book packs**: when a grabbed release turns out to be a bundle
  ("complete series"), the grabbed book's file is identified by volume
  number (comics), title (ebooks), or top-level folder name
  (audiobooks — each book its own subfolder of tracks) — never by size, so a
  v01–v12 pack can't file volume 12 as the one you grabbed. With **Import
  whole packs** on (the default), the pack's other files fill every book
  they match — imported ebooks/audiobooks join their format library, though
  nothing is monitored automatically. Turn it off to fill only the grabbed
  book plus other **monitored** books. Either way, a book that already owns
  the format is only replaced when the pack's copy is a genuine quality
  upgrade. An audiobook bundle needs at least two distinctly-named folders
  with nothing loose in the root to be recognized as a pack at all —
  anything less structured (a single folder, flat tracks, disc/part
  subfolders like CD1/CD2 at the root) imports as one ordinary multi-file
  audiobook instead, exactly as it always has.
- **Seed goals**: with *remove completed downloads* off, a torrent keeps
  seeding to the ratio/time limit set in qBittorrent; when it finishes and
  pauses (goal reached), LibriNode removes the torrent *and its data* — but
  only for downloads it grabbed and imported.
- **Failed and junk downloads** are blocklisted (never grabbed again; a
  replacement search starts immediately) and deleted — out of the client and
  off disk. This covers client-side failures, spam whose content isn't the
  book (an `.exe` instead of a media file), and completed downloads whose
  files never become readable. The blocklist is managed from the Activity
  page.
- **Removing a download** from the Activity queue deletes it (and its data)
  from the client and resolves its pending grab, without blocklisting it — the
  release can be grabbed again right away. If a grab is ever stuck reporting
  "pending" with no matching entry left in the queue above (its download is
  already gone from the client), **Activity → History** shows a **cancel**
  button on that entry: it clears LibriNode's own record directly, unblocking
  a new search or grab for that book.

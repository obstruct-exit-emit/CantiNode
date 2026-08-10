# Acquisition

Acquisition serves the **Music** library.

## Indexers

Two ways in:

- **Manually** under **Settings → Indexers**: any Newznab (usenet) or
  Torznab (torrent) endpoint, including per-indexer feed URLs from Prowlarr
  or Jackett. Test buttons on the form and on every saved indexer.
- **Prowlarr sync**: in Prowlarr, add an application of type **Readarr**
  with CantiNode's URL and API key. Prowlarr pushes its indexers into
  CantiNode and keeps them in sync (CantiNode emulates the Readarr v1 API).

Each indexer carries an **audio category list** (default `3010,3040`) —
adjust per indexer if yours differ.

An indexer that keeps failing **rests with exponential backoff** (5 minutes
doubling up to 6 hours) instead of being retried every sweep; one success
clears it.

### Native indexers

Some sites speak no Newznab/Torznab API, so Prowlarr structurally can't reach
them. A **native** indexer is a built-in source, selected as the indexer's
*type* under **Settings → Indexers** (no URL to paste) — it feeds the same
search, scoring, and grab pipeline as everything else. Native indexers are
CantiNode-managed only and are hidden from Prowlarr, so it never treats them as
indexers it owns. No native sources ship built in today; the framework stays
in the tree for a future source to register against.

### The `direct` protocol

Some sources hand out plain HTTP links, not torrents or NZBs, so CantiNode has
its own **direct** download client — add it under **Settings → Download
Clients** with a local download folder as its "host". It streams the file
itself, **failing over across a `|`-separated mirror list**, following a
membership-API JSON answer or an open-mirror landing page one hop to the real
file. The saved file is named by its **actual content**, so a link that
resolves to a `get.php`-style URL is still written with the right extension
(not the `.php` the URL implies); a mirror that answers with an error or
landing page instead of the file is rejected rather than saved as bogus
media; and because the file is streamed only to be scanned in, it's removed
from the download folder afterward. It's source-agnostic — any native
indexer that resolves to plain HTTP links can ride it — but with no native
sources shipping today, there's currently nothing that grabs through it.

## Release scoring & quality profiles

Search results are parsed (formats, size) and scored against the **default
quality profile** (**Settings → Quality Profiles**): ordered format
preferences, size bounds. Candidates that can't be a real release are
rejected outright (an executable/installer named outright, no seeders on a
torrent, no download link).

With **upgrades allowed**, owning a lesser format keeps the album wanted
until the profile's cutoff; upgrade grabs must be strictly better.

## Download clients

qBittorrent (torrents) and SABnzbd (usenet), under **Settings → Download
Clients** — category-scoped so CantiNode only ever touches its own
downloads.

CantiNode resolves each release on **its own side** before handing it off:
it fetches the NZB and uploads the file (SABnzbd `addfile`), and follows a
torrent to its magnet or downloads the `.torrent` and uploads the bytes. So a
download client behind NAT — or a SABnzbd/qBittorrent-compatible **debrid
bridge** (Real-Debrid, TorBox) whose cloud side can't reach your LAN
indexers — still works. Adds to a slow debrid bridge are confirmed against the
client's list so a slow response never loses the grab. A torrent grab is
tracked by the **magnet's own info hash**, not by name: some bridges (TorBox
included) ignore CantiNode's rename request and always report the
uploader's own torrent name instead — sometimes wildly different from, or a
typo of, the release title — so tracking by hash keeps the queue's album
linking working regardless of what name the bridge shows.

- An artist's **Search wanted** and per-item **Auto grab** run on demand;
  **Search releases** lists scored candidates for hand-picking. There is no
  automatic background search sweep today — acquisition is user-triggered.
- Grabbed files land in your download client's output folder like any other
  file. **They aren't imported automatically** — the next **Scan files**
  pass (on the Music page, or an artist's own page) matches them against
  MusicBrainz and, if **Organize on match** is enabled (**Settings →
  General**, off by default), moves them into the naming-template layout
  right away; otherwise run **Organize…** yourself once you've reviewed the
  scan.
- **Failed and junk downloads** are blocklisted (never grabbed again; a
  replacement search starts immediately) and deleted — out of the client and
  off disk. This covers client-side failures and spam whose content isn't
  the media (an `.exe` instead of an audio file). The blocklist is managed
  from the Activity page.
- **Removing a download** from the Activity queue deletes it (and its data)
  from the client and resolves its pending grab, without blocklisting it — the
  release can be grabbed again right away. If a grab is ever stuck reporting
  "pending" with no matching entry left in the queue above (its download is
  already gone from the client), **Activity → History** shows a **cancel**
  button on that entry: it clears CantiNode's own record directly, unblocking
  a new search or grab for that album.

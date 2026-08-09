# Quickstart

1. **Connect.** Open `http://localhost:7845`. A brand-new instance greets you
   with a **first-run setup wizard** — create an account (no API key needed)
   and it walks you through libraries, metadata, an indexer, and a download
   client. Otherwise, paste the API key from `config.yaml` in the data
   directory, or add a login account later under **Settings → General →
   Security** and sign in with a username/password instead.

2. **Metadata.** Under **Settings → Metadata**, paste your
   [Hardcover API token](https://hardcover.app/account/api), hit **Test**,
   then **Save**. Search goes live immediately. Manga metadata comes from
   AniList (no key) or Hardcover; comics from Hardcover (the default) or a
   free [ComicVine key](https://comicvine.gamespot.com/api/) — pick each
   provider on the same page.

3. **Root folders.** Under **Settings → Media Management**, add one root
   folder per media type you use. Adding a root folder is what makes that
   library appear in the sidebar.

4. **Add something.** On a library page, hit **+ Add** and search. Adding an
   author pulls their bibliography as metadata and joins the library,
   but monitors nothing yet — every book starts in that author's **Missing**
   section for you to monitor selectively. Adding a specific book pulls its
   editions and monitors just that one. Manga/comic series work like
   authors: adding one pulls its volumes as metadata, all starting in the
   series' **Missing** section — monitor volumes selectively, or flip the
   series' monitor toggle to monitor everything (including future volumes).
   Magazines are added by name and are organize-only for now — scanning and
   organizing work, downloading is disabled.

5. **Scan what you own.** **Scan files** on a library page matches existing
   files to your books — every item gets an owned/wanted badge. Strays land
   in an unmatched list with a confidence-rated best guess: import them in
   one click (or all confident matches at once), resolve duplicates, or add
   the missing author/series/magazine right from the row.

6. **Automate acquisition.** Add indexers (**Settings → Indexers**, or sync
   them from Prowlarr by adding LibriNode as a *Readarr* application — plus
   optional built-in **native sources** for sites Prowlarr can't reach) and a
   download client (**Settings → Download Clients**, with **Test** buttons).
   Monitored items are searched automatically every six hours; **Search
   wanted**, per-item **Auto grab**, and interactive **Search releases**
   cover "right now". Finished downloads import, rename, and organize
   themselves; the **Activity** page shows the queue and history.

7. **Check the Calendar** for upcoming releases, the per-library **Wanted**
   card for gaps, and **System** for health checks, logs, and backups.

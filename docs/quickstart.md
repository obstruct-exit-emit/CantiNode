# Quickstart

1. **Connect.** Open `http://localhost:7847`. A brand-new instance greets you
   with a **first-run setup wizard** — create an account (no API key needed)
   and it walks you through your music folder, an indexer, and a download
   client. Otherwise, paste the API key from `config.yaml` in the data
   directory, or add a login account later under **Settings → General →
   Security** and sign in with a username/password instead.

2. **Root folder.** Under **Settings → Media Management**, add the root
   folder your music lives in. Adding it is what makes the Music library
   appear in the sidebar.

3. **Add an artist.** On the Music page, hit **+ Add**, search MusicBrainz,
   and pick the right one. Adding (monitoring) an artist caches their whole
   discography, bio, and photo right away — every release starts in that
   artist's **Missing** section for you to want selectively.

4. **Scan what you own.** **Scan files** on the Music page (or an artist's
   own page) matches existing files against MusicBrainz — by embedded
   `MUSICBRAINZ_TRACKID`/`MUSICBRAINZ_ALBUMID` tags first, then whole-folder
   release matching, then fuzzy title search. Files it can't confidently
   place land in an **Unmatched** list for manual review. A newly-discovered
   artist (one your files matched to, that you hadn't explicitly monitored)
   gets its metadata cached automatically too.

5. **Automate acquisition.** Add indexers under **Settings → Indexers** —
   Newznab/Torznab by hand, or one **Prowlarr** connection to search
   everything Prowlarr already has configured — and a download client
   (**Settings → Download Clients**, with **Test** buttons).
   From an artist's **Missing** section, **+ Add** (or **+ Add & Monitor**)
   an album to want it — it now shows in the **Albums** grid badged
   "wanted". A monitored artist's wanted albums are searched and grabbed
   automatically on a schedule (default daily — **Settings → General →
   Background timings**); to do it yourself right away, click into the
   wanted album and use **Search releases** to pick a specific one. Either
   way, once a download finishes it's copied into the library and scanned
   in on its own — no manual **Scan files** needed.

6. **Check Activity** for the download queue and grab history, and
   **System** for health checks, logs, and backups.

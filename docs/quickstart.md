# Quickstart

1. **Build and run** — see [Installation](installation.md).

   ```sh
   make build
   ./cantinode
   ```

   The first run generates a random API key and prints it to the log:

   ```
   level=INFO msg="api key for the native API" api_key=<your key>
   ```

   It's also written to `config.yaml` (`api_key`) — copy it from either
   place.

2. **Open the web UI** at `http://localhost:7847` and paste the API key
   in when prompted. It's stored in the browser (localStorage) after
   that, so you won't need to re-enter it on this device.

3. **Add a root folder** — the Root Folders tab, pointed at a directory
   of music you already have on disk. The path must already exist;
   CantiNode organizes an existing library, it doesn't create one.

4. **Scan** — click "Scan now" (top right), or just wait: a scan also
   runs automatically on startup and then every `scan_interval_hours`
   (default 6). CantiNode walks the root folder, reads each audio file's
   tags, and matches it against MusicBrainz:
   - A file whose tags already carry a MusicBrainz recording ID (common —
     Picard and most rippers embed these) matches immediately, with full
     confidence.
   - Otherwise CantiNode fuzzy-searches MusicBrainz by artist/album/title
     and accepts the result only if it scores above
     `min_match_confidence` (default 0.75) — see
     [Configuration](configuration.md).

5. **Review Unmatched** — anything the scan couldn't confidently match
   lands in the Unmatched tab. Search MusicBrainz yourself (prefilled
   from whatever tags the file did have) and pick the right recording to
   link it by hand.

6. **Browse the Library** — matched artists → albums → tracks. Each track
   shows its file(s) with a **Preview** (see exactly where it would move
   to, with no changes yet), **Organize** (actually rename/move it), and
   — for MP3/FLAC — **Write tags** (embed the matched metadata into the
   file's own tags) action, using the naming format from Settings.

7. **(Optional) Set up acquisition** — Settings → Acquisition, if you run
   a [Prowlarr](https://prowlarr.com) and/or
   [AcerviNode](https://github.com/obstruct-exit-emit/AcerviNode)
   instance. Then, in the Wanted tab: monitor an artist (searches
   MusicBrainz by name), which seeds their studio albums as wanted; **Find
   release** searches Prowlarr for one, and **Grab** sends your pick to
   AcerviNode. Grabbing is always a manual click — nothing downloads on
   its own. A finished download is imported into the library
   automatically once AcerviNode reports it done.

Everything here is also a plain REST call under `/api/v1` — see
[API](api.md) if you'd rather script it.

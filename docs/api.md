# API

CantiNode's native API is versioned at `/api/v1` — it's the exact API the
embedded web UI runs on, so anything the UI does is scriptable.

## Authentication

Every route except `GET /api/v1/health` requires
`Authorization: Bearer <api_key>`, checked against the live config (a key
regenerated in `config.yaml` takes effect immediately, no restart). See
[Configuration](configuration.md).

```sh
curl -H "Authorization: Bearer $API_KEY" http://localhost:7847/api/v1/root-folders
```

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/health` | Liveness check, unauthenticated |
| GET | `/api/v1/version` | Build version |
| GET | `/api/v1/root-folders` | List root folders |
| POST | `/api/v1/root-folders` | Add a root folder — body `{"path": "..."}`, path must already exist |
| DELETE | `/api/v1/root-folders/{id}` | Remove a root folder (its scanned files are forgotten; nothing on disk is touched) |
| GET | `/api/v1/browse-directories?path=` | List `path`'s subdirectories, on the server's own filesystem — powers the Root Folders "Browse..." picker. An empty/absent `path` lists top-level roots instead (drive letters on Windows, `/` elsewhere) |
| GET | `/api/v1/artists` | List artists with at least one matched file |
| GET | `/api/v1/artists/{id}/albums` | List an artist's albums |
| GET | `/api/v1/albums/{id}/tracks` | List an album's tracks |
| GET | `/api/v1/tracks/{id}/files` | List the file(s) matched to a track |
| GET | `/api/v1/albums/{id}/cover` | Album front cover image — accepts the API key via `?api_key=` too, since an `<img src>` can't send an `Authorization` header (see [Configuration](configuration.md)); 404 if the release has no cover art |
| GET | `/api/v1/track-files/unmatched` | List files awaiting manual review |
| POST | `/api/v1/track-files/{id}/match` | Manually link a file to a MusicBrainz recording — body `{"recording_mbid": "...", "release_mbid": "..."}` (`release_mbid` optional) |
| DELETE | `/api/v1/track-files/{id}/match` | Unlink a file, moving it back to unmatched (file on disk untouched) |
| DELETE | `/api/v1/track-files/{id}` | Permanently delete a file — off disk and out of the database |
| GET | `/api/v1/track-files/{id}/organize/preview` | Compute (without moving) where a matched file would be organized to |
| POST | `/api/v1/track-files/{id}/organize` | Actually move/rename a matched file per `naming_format` |
| POST | `/api/v1/track-files/{id}/write-tags` | Embed the file's matched metadata into its own tags (MP3/FLAC only) |
| GET | `/api/v1/musicbrainz/search?artist=&album=&title=` | Fuzzy MusicBrainz recording search (any params optional, at least one needed) — what the Unmatched review UI uses |
| POST | `/api/v1/scan` | Start a full scan (every root folder) in the background — 409 if one's already running |
| GET | `/api/v1/scan/status` | Current/last scan's status |
| GET | `/api/v1/musicbrainz/artist-search?query=` | Fuzzy MusicBrainz artist search — what "Monitor an artist" uses to resolve a name to an MBID |
| GET | `/api/v1/artists/{id}` | One artist's detail — bio/image (cached from TheAudioDB), monitoring state, `owned_album_count` |
| POST | `/api/v1/artists/monitor` | Start monitoring an artist by MBID — body `{"mbid": "..."}`; caches their entire discography (see [Configuration](configuration.md)) but wants nothing automatically |
| POST | `/api/v1/artists/{id}/monitor` | Start monitoring an already-known artist (already owned and/or previously unmonitored) |
| POST | `/api/v1/artists/{id}/unmonitor` | Stop monitoring — just flips the flag; owned albums, wanted albums, and in-flight downloads are untouched |
| POST | `/api/v1/artists/{id}/refresh-metadata` | Re-fetch the artist's cached discography from MusicBrainz and bio/image from TheAudioDB |
| GET | `/api/v1/artists/{id}/missing` | Cached release groups not yet owned or wanted — the unified artist page's "Missing" section |
| POST | `/api/v1/artists/{id}/wanted` | Want one release group from the cached discography — body `{"release_group_mbid": "..."}` ("Add"); the UI separately calls monitor for "Add & Monitor" |
| GET | `/api/v1/artists/{id}/wanted` | List an artist's wanted albums |
| POST | `/api/v1/wanted-albums/{id}/ignore` | Mark a wanted album as ignored |
| GET | `/api/v1/wanted-albums/{id}/search` | Search Prowlarr for this wanted album — 400 if Prowlarr isn't configured |
| POST | `/api/v1/wanted-albums/{id}/grab` | Grab a release chosen from the search results (body: the release object as returned by search) via qBittorrent or SABnzbd, whichever matches the release's protocol — 400 if Prowlarr or the matching download client isn't configured, or no root folder exists yet |
| GET | `/api/v1/downloads` | List every tracked download, most recent first |
| DELETE | `/api/v1/downloads/{id}` | Cancel a grab — best-effort removes it from its download client and reverts the wanted album back to `wanted` |
| GET | `/api/v1/settings` | Current settings (includes `api_key` and, if set, `prowlarr_api_key`/`qbittorrent_password`/`sabnzbd_api_key`/`audiodb_api_key`) |
| PUT | `/api/v1/settings` | Update settings (`port` in the body is ignored — see [Configuration](configuration.md)) |

All request/response bodies are JSON. A scan (`POST /api/v1/scan`) runs
asynchronously — MusicBrainz's rate limit means a real scan of an
unmatched library can take minutes, so the request returns immediately
(`202 Accepted`) and `GET /api/v1/scan/status` is polled for progress, the
same way the web UI does. Grabbing a release is synchronous (the add
itself is fast) but importing it isn't — a background poll picks up a
finished download and imports it automatically; `GET /api/v1/downloads`
reflects its status (`downloading` → `completed` → `imported`, or
`error`) as that happens.

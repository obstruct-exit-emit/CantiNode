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
| GET | `/api/v1/artists` | List artists with at least one matched file |
| GET | `/api/v1/artists/{id}/albums` | List an artist's albums |
| GET | `/api/v1/albums/{id}/tracks` | List an album's tracks |
| GET | `/api/v1/tracks/{id}/files` | List the file(s) matched to a track |
| GET | `/api/v1/track-files/unmatched` | List files awaiting manual review |
| POST | `/api/v1/track-files/{id}/match` | Manually link a file to a MusicBrainz recording — body `{"recording_mbid": "...", "release_mbid": "..."}` (`release_mbid` optional) |
| DELETE | `/api/v1/track-files/{id}/match` | Unlink a file, moving it back to unmatched |
| GET | `/api/v1/track-files/{id}/organize/preview` | Compute (without moving) where a matched file would be organized to |
| POST | `/api/v1/track-files/{id}/organize` | Actually move/rename a matched file per `naming_format` |
| GET | `/api/v1/musicbrainz/search?artist=&album=&title=` | Fuzzy MusicBrainz recording search (any params optional, at least one needed) — what the Unmatched review UI uses |
| POST | `/api/v1/scan` | Start a full scan (every root folder) in the background — 409 if one's already running |
| GET | `/api/v1/scan/status` | Current/last scan's status |
| GET | `/api/v1/settings` | Current settings (includes `api_key`) |
| PUT | `/api/v1/settings` | Update settings (`port` in the body is ignored — see [Configuration](configuration.md)) |

All request/response bodies are JSON. A scan (`POST /api/v1/scan`) runs
asynchronously — MusicBrainz's rate limit means a real scan of an
unmatched library can take minutes, so the request returns immediately
(`202 Accepted`) and `GET /api/v1/scan/status` is polled for progress, the
same way the web UI does.

# API

Everything the UI does goes through the versioned REST API at `/api/v1` —
same endpoints, fully scriptable. Authenticate with the `X-Api-Key` header
(or `?apikey=`), or a login session cookie.

```sh
curl -H "X-Api-Key: <key>" http://localhost:7847/api/v1/system/status
```

| Area | Endpoints |
|---|---|
| System | `GET /system/status`, `GET /ping` (no auth), `GET /health`, `POST /health/check`, `GET /log?lines=N`, `GET /image?url=` (cached provider-image proxy), `DELETE /cache` (clear the image cache) |
| Auth | `GET /auth/status` + `POST /auth/login` (unauthenticated), `POST /auth/logout`, `PUT /auth/credentials` (create/change one account; empty username disables all), `GET/POST /auth/users` (adds take `"role": "admin"\|"member"`, default member), `DELETE /auth/users/{username}` (not the default), `PUT /auth/users/{username}/password` (self-service: admin, or the same user), `PUT /auth/users/{username}/role` (`admin`\|`member`; the default user stays admin), `PUT /auth/users/{username}/default`, `POST /auth/apikey/regenerate` |
| Setup | `GET /setup/status` (unauthenticated — is this a fresh instance?), `POST /auth/setup` (first-run wizard: claim a fresh instance, create the default account, no API key needed) |
| Backups | `GET/POST /backup`, `DELETE /backup/{name}`, `POST /backup/{name}/restore`, `GET /backup/{name}/download` |
| Root folders | `GET/POST /rootfolder`, `DELETE /rootfolder/{id}`, `GET /filesystem?path=` (folder picker: lists a directory's subfolders and its parent; empty path starts at the filesystem root) |
| Artists | `GET /music/artist`, `GET /music/artist/search?term=` (MusicBrainz), `POST /music/artist` (`{"mbid": "..."}` — monitor, pulls the discography as metadata only), `GET /music/artist/{id}`, `POST /music/artist/{id}/unmonitor`, `DELETE /music/artist/{id}` (`?deleteFiles=true`), `POST /music/artist/{id}/refresh` (metadata only), `GET /music/artist/{id}/missing` (discography gaps), `GET /music/artist/{id}/albums`, `POST /music/artist/{id}/wanted` `{"releaseGroupMbid": "...", "monitor": bool}` (want an album, optionally monitoring the artist too), `GET /music/artist/{id}/wanted` (list), `GET /music/artist/{id}/organize/preview`, `POST /music/artist/{id}/organize` — the artist-level scan is `POST /music/scan` below (library-wide, not artist-scoped) |
| Albums & tracks | `GET /music/album/{id}`, `GET /music/album/{id}/tracks`, `GET /music/album/{id}/cover` (accepts `?apikey=` for `<img src>` use), `DELETE /music/album/{id}` (`?deleteFiles=true` — just this album, not the whole artist), `GET /music/album/{id}/organize/preview`, `POST /music/album/{id}/organize`, `POST /music/album/{id}/scan` (scoped to just this album's own folder, unlike the library-wide scan), `GET /music/album/{id}/upgrade/search` (scored candidates better than what's owned — 400 if upgrades aren't enabled or the album's already at the profile's cutoff), `POST /music/album/{id}/upgrade/grab`, `GET /music/track/{id}/files` |
| Scanning & matching | `POST /music/scan` (scans every root folder), `GET /music/scan/status`, `GET /music/trackfile/unmatched`, `POST /music/trackfile/{id}/match` `{"recordingMbid": "...", "releaseMbid": "..."}`, `DELETE /music/trackfile/{id}/match`, `GET /music/trackfile/{id}/organize/preview`, `POST /music/trackfile/{id}/organize`, `POST /music/trackfile/{id}/write-tags`, `DELETE /music/trackfile/{id}` (`?deleteFiles=true`) |
| Search & grab | `GET /music/musicbrainz/search?term=` (raw recording search), `GET /music/releasegroup/{mbid}/tracks`, `DELETE /music/wanted/{id}` (stop wanting — falls back to Missing), `GET /music/wanted/{id}/search` (scored release candidates), `POST /music/wanted/{id}/grab` |
| Quality | `GET/POST /qualityprofile`, `PUT/DELETE /qualityprofile/{id}`, `PUT /qualityprofile/{id}/default` |
| Indexers | `GET/POST /indexer` (type `newznab`\|`torznab`\|a native source name — `prowlarr` ships built in: search a self-hosted Prowlarr instance directly instead of duplicating each of its indexers here), `GET/PUT/DELETE /indexer/{id}`, `POST /indexer/test`, `GET /indexer/native` (native source catalog: name, protocol, media types, URL/key requirements) |
| Downloads | `GET/POST /downloadclient` (types: `qbittorrent`, `sabnzbd`, `direct` — the built-in HTTP fetcher, whose `host` is a local download folder), `PUT/DELETE /downloadclient/{id}`, `POST /downloadclient/test`, `GET /queue` (each item enriched with its grab and live progress), `DELETE /queue/{clientId}/{itemId}` (remove one download + its data, no blocklist), `POST /grab/{id}/cancel` (manually resolve a stuck "pending" grab, without touching any client), `GET /history?search=&limit=&offset=` (paged: `{"records": […], "total": N}`), `GET /blocklist`, `DELETE /blocklist/{id}` |
| Settings | `GET/PUT /settings/naming` (the music path template), `GET/PUT /settings/music` (organize-on-match, match confidence, MusicBrainz/TheAudioDB keys), `GET/PUT /settings/timings` (health check + wanted-list sweep cadences; 0 = default, clamped, applied at startup), `GET/PUT /settings/pathmappings` (remote→local download-path prefixes) |

Notes:

- `POST /music/artist` monitors an artist and pulls their discography as
  metadata only — nothing is auto-monitored, so every release starts in
  that artist's Missing section (`GET /music/artist/{id}/missing`) until
  you `POST /music/artist/{id}/wanted` it individually.
- **Admin vs. member**: every server-configuration and account-management route
  (all `/settings/*`, `/indexer*`, `/downloadclient*`, `/qualityprofile*`,
  `/rootfolder*`, `/backup*`, `/log`, `/filesystem`, `/cache`, and the
  `/auth/users` management endpoints) requires an **admin** session and
  returns 403 for a member. Content routes (search, grab, scan, library
  browsing) are open to any authenticated user. A valid API key is
  admin-equivalent, so scripts are unaffected. `PUT
  /auth/users/{username}/password` is the one exception — a member may
  change their own password.
- Two loops run on their own schedule, no endpoint call needed: a
  Completed-Download-Handling poll (copies a finished grab's audio files
  into the library and scans them in) and a wanted-list sweep for monitored
  artists (`internal/autosearch` — searches and grabs, tunable via
  `/settings/timings`), alongside the health check. Manual search, grab,
  scan, and organize are still available as ordinary endpoint calls
  whenever you want them regardless.

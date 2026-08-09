# API

Everything the UI does goes through the versioned REST API at `/api/v1` —
same endpoints, fully scriptable. Authenticate with the `X-Api-Key` header
(or `?apikey=`), or a login session cookie.

```sh
curl -H "X-Api-Key: <key>" http://localhost:7845/api/v1/system/status
```

| Area | Endpoints |
|---|---|
| System | `GET /system/status`, `GET /ping` (no auth), `GET /health`, `POST /health/check`, `GET /log?lines=N`, `GET /image?url=` (cached provider-image proxy) |
| Auth | `GET /auth/status` + `POST /auth/login` (unauthenticated), `POST /auth/logout`, `PUT /auth/credentials` (create/change one account; empty username disables all), `GET/POST /auth/users` (adds take `"role": "admin"\|"member"`, default member), `DELETE /auth/users/{username}` (not the default), `PUT /auth/users/{username}/password` (self-service: admin, or the same user), `PUT /auth/users/{username}/role` (`admin`\|`member`; the default user stays admin), `PUT /auth/users/{username}/default`, `POST /auth/apikey/regenerate` |
| Setup | `GET /setup/status` (unauthenticated — is this a fresh instance?), `POST /auth/setup` (first-run wizard: claim a fresh instance, create the default account, no API key needed) |
| Backups | `GET/POST /backup`, `DELETE /backup/{name}`, `POST /backup/{name}/restore`, `GET /backup/{name}/download` |
| Root folders | `GET/POST /rootfolder` (manga roots take a `"variant"`: `color`\|`mono`, default `mono`), `DELETE /rootfolder/{id}`, `GET /filesystem?path=` (folder picker: lists a directory's subfolders and its parent; empty path starts at the filesystem root) |
| Search | `GET /search?term=&type=author\|book\|manga\|comic` |
| Authors | `GET/POST /author` (`?library=` scopes; adds take `"library"`), `GET/DELETE /author/{id}` (`?deleteFiles=true`, every library), `PUT /author/{id}/library` (add/remove from ONE format library; `deleteFiles`; auto-deletes the author once in no library), `PUT /author/{id}/monitor`, `PUT /author/{id}/provider` (per-author provider override; `""` follows settings), `POST /author/{id}/refresh` (metadata only — never touches membership/monitoring), `GET /author/{id}/missing?library=` (bibliography gaps), `POST /author/{id}/search?library=` (this author's wanted books only) |
| Books | `GET/POST /book`, `GET/DELETE /book/{id}` (`?deleteFiles=true`), `GET /book/{id}/cover` (cover from the owned CBZ/CBR's first page; cached under `<data>/covers`), `PUT /book/{id}/library` (membership + monitored + `deleteFiles`; `library:"manga"`/`"comic"` adds/removes a volume/issue, `member:false` forgets its file records so it drops to Missing), `PUT /book/{id}/monitor`, `POST /book/{id}/refresh` |
| Series | `GET/POST /series`, `GET/DELETE /series/{id}` (`?deleteFiles=true`), `PUT /series/{id}/monitor`, `PUT /series/{id}/provider` (per-series provider override; `""` follows settings), `POST /series/{id}/refresh`, `POST /series/{id}/search` (this series' wanted volumes only; magazines answer with an organize-only notice) |
| Libraries | `GET /libraries`, `GET /home`, `GET /wanted?library=X`, `GET /calendar?past=30&days=90` |
| Files | `POST /library/scan` (`?mediaType=` scopes to one library's roots; absent scans all), `POST /library/refresh` (`{"mediaType":"…"}` — background metadata re-sync of the whole library, one at a time; magazines refused),  `GET/POST /library/rename` (preview/apply; `?bookId=`, `?authorId=`/`{"authorId":N}`, `?seriesId=`/`{"seriesId":N}`, or `?mediaType=`/`{"mediaType":"…"}` — one library — scopes, otherwise everything), `GET /bookfile?unmatched=true`, `GET /bookfile/unmatched/options?mediaType=` (existing-file import: per-file suggestion + 0–100 confidence, candidates, duplicate info — all five media types), `POST /bookfile/import-matched` `{"mediaType":"…"}` (bulk-import every confident match), `POST /bookfile/{id}/match` (`{"bookId":N}`, or `{"seriesId":N,"issue":"…"}` to materialize a magazine issue), `POST /bookfile/{id}/replace` `{"bookId":N}` (this file replaces the book's owned copy, which is deleted; manga replaces only the matching variant), `DELETE /bookfile/{id}` (`?deleteFiles=true` also deletes from disk), `DELETE /library/covers/cache` (clear extracted comic covers) |
| Indexers | `GET/POST /indexer` (native rows are hidden when the caller is Prowlarr), `GET/PUT/DELETE /indexer/{id}`, `POST /indexer/test`, `GET /indexer/schema`, `GET /indexer/native` (built-in native source catalog: name, protocol, media types, URL/key needs), `GET /release?term=` or `?bookId=N&mediaType=` (magazines rejected — organize-only), `GET /release/packs?seriesId=N` (whole-series pack candidates for manga/comics; grab one via the normal grab endpoint with the returned `grabBookId`) |
| Quality | `GET/POST /qualityprofile`, `PUT/DELETE /qualityprofile/{id}`, `PUT /qualityprofile/{id}/default` |
| Downloads | `GET/POST /downloadclient` (types: `qbittorrent`, `sabnzbd`, `direct` — the built-in HTTP fetcher, whose `host` is a local download folder), `PUT/DELETE /downloadclient/{id}`, `POST /downloadclient/test`, `POST /release/grab` (protocols `torrent`\|`usenet`\|`direct`; magazines rejected — organize-only), `GET /queue` (each item enriched with its book/grab and live progress; short-cached snapshot), `DELETE /queue/{clientId}/{itemId}` (remove one download + its data, no blocklist), `POST /grab/{id}/cancel` (manually resolve a pending grab as failed, without touching any client — for one whose queue entry is already gone and stuck reporting "pending"), `GET /history?search=&limit=&offset=` (paged: `{"records": […], "total": N}`), `POST /library/import`, `GET /blocklist`, `DELETE /blocklist/{id}` |
| Auto search | `POST /book/{id}/search?mediaType=`, `POST /library/search` |
| Settings | `GET/PUT /settings/metadata`, `POST /settings/metadata/test`, `DELETE /settings/metadata/cache` (clear provider images), `DELETE /settings/metadata/descriptions` (blank stored descriptions), `DELETE /cache` (clear all caches), `GET/PUT /settings/naming`, `GET/PUT /settings/import`, `GET/PUT /settings/timings` (background cadences; 0 = default, clamped, applied at startup), `GET/PUT /settings/pathmappings` (remote→local download-path prefixes) |

Notes:

- `POST /author` takes `{"foreignAuthorId": "...", "library": "ebook"}` and
  pulls the bibliography as metadata (canonical works by Hardcover
  readership) — the author joins the library, but no book is enrolled or
  monitored (see Missing, below). `POST /book`
  takes `foreignBookId` the same way and monitors + enrolls that one book.
  `POST /series` takes `{"mediaType": "manga", "foreignSeriesId": ...}`
  or `{"mediaType": "magazine", "title": "..."}` — like author adds it pulls
  metadata only (volumes start unmonitored, in the series' Missing section);
  pass `"monitored": true` for the monitor-everything behavior.
- The per-author **Missing** section (bibliography gaps not yet monitored
  or owned in a format library) is `GET /author/{id}/missing?library=`;
  one-click monitor is the existing `PUT /book/{id}/library`.
- The Prowlarr-facing surface emulates Readarr v1 (`/api/v1/indexer` accepts
  Readarr resources; `/system/status` reports a Readarr-compatible
  `version`, LibriNode's own in `appVersion`). During application sync
  Prowlarr also reads `/api/v1/rootfolder`, `/qualityprofile`,
  `/metadataprofile` (Readarr-only), and `/downloadclient` — these return
  Readarr-shaped resources (download clients carry `protocol` so torrent
  indexers sync) when the caller's User-Agent is Prowlarr, native JSON
  otherwise.
- Book metadata (`/search?type=book`, `POST /author`/`/book`) is served by the
  active provider with the configured fallbacks behind it: a fallback answers
  only when the active provider finds nothing, and a record found that way is
  stored under the fallback's name so its later refresh routes back to the same
  source.
- **Admin vs. member**: every server-configuration and account-management route
  (all `/settings/*`, `/indexer*`, `/downloadclient*`, `/qualityprofile*`,
  `/rootfolder*`, `/backup*`, `/log`, `/filesystem`, and the `/auth/users`
  management endpoints) requires an **admin** session and returns 403 for a
  member. Content routes (search, grab, scan, library browsing) are open to any
  authenticated user. A valid API key is admin-equivalent, so scripts and
  Prowlarr are unaffected. `PUT /auth/users/{username}/password` is the one
  exception — a member may change their own password.
- Without a metadata token, metadata endpoints return 503.

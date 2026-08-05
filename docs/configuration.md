# Configuration

CantiNode reads `config.yaml` (path from `CANTINODE_CONFIG`, default
`./config.yaml`) on startup, then applies `CANTINODE_*` environment
overrides on top. A first run with no config file writes sensible
defaults plus a freshly generated `api_key`.

Root folders are **not** part of `config.yaml` — they're runtime-editable
library state, stored in the database, managed through the API/UI's Root
Folders tab instead.

| YAML key | Env override | Default | Notes |
|---|---|---|---|
| `port` | `CANTINODE_PORT` | `7847` | HTTP port for the API and web UI |
| `data_dir` | `CANTINODE_DATA_DIR` | `./data` | Where the SQLite database lives |
| `api_key` | `CANTINODE_API_KEY` | *(random)* | `Authorization: Bearer <key>` for every `/api/v1` request |
| `log_level` | `CANTINODE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `scan_interval_hours` | `CANTINODE_SCAN_INTERVAL_HOURS` | `6` | How often the background scan loop runs (a scan also always runs once at startup) |
| `naming_format` | `CANTINODE_NAMING_FORMAT` | `{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}` | Organizer's file naming template — see below |
| `organize_on_match` | `CANTINODE_ORGANIZE_ON_MATCH` | `false` | Move/rename a file immediately once matched, instead of requiring an explicit Organize action |
| `min_match_confidence` | `CANTINODE_MIN_MATCH_CONFIDENCE` | `0.75` | Minimum MusicBrainz search score (0–1) to auto-accept a fuzzy match; a direct MBID match always wins regardless |
| `musicbrainz_contact_email` | `CANTINODE_MUSICBRAINZ_CONTACT_EMAIL` | *(empty)* | Included in CantiNode's MusicBrainz User-Agent, per [MusicBrainz's API usage policy](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) — optional, but recommended |
| `prowlarr_url` | *(none)* | *(empty)* | Base URL of a self-hosted Prowlarr instance, e.g. `http://localhost:9696` — see [Acquisition](#acquisition) |
| `prowlarr_api_key` | *(none)* | *(empty)* | Prowlarr's own API key |
| `qbittorrent_url` | *(none)* | *(empty)* | Base URL of a qBittorrent-Web-API-compatible server, e.g. `http://localhost:8080` |
| `qbittorrent_username` | *(none)* | *(empty)* | Username for that server's Web API login |
| `qbittorrent_password` | *(none)* | *(empty)* | Password for that server's Web API login |
| `sabnzbd_url` | *(none)* | *(empty)* | Base URL of a SABnzbd-API-compatible server, e.g. `http://localhost:8085` |
| `sabnzbd_api_key` | *(none)* | *(empty)* | That server's own API key |

Everything except `port` and `data_dir` can also be changed live through
Settings → the web UI (or `PUT /api/v1/settings`) — `naming_format`,
`min_match_confidence`, `organize_on_match`, and the Prowlarr/qBittorrent/
SABnzbd connection details all take effect immediately, no restart needed.
`port` requires editing `config.yaml` (or the env var) and restarting.

## Naming format

The organizer (Library → a matched track's file → **Organize**, or
automatically if `organize_on_match` is on) renames/moves a file according
to this template. Supported placeholders:

| Placeholder | Example |
|---|---|
| `{Artist}` | `Boards of Canada` |
| `{Album}` | `Geogaddi` |
| `{Year}` | `2002` (from the album's release date) |
| `{TrackNumber}` | `03` (zero-padded to 2 digits) |
| `{DiscNumber}` | `1` |
| `{Title}` | `Alpha and Omega` |
| `{Ext}` | `flac` |

Every placeholder value is sanitized independently (illegal filename
characters like `:` `/` `?` become `_`) — the format string's own `/`
separators are what create subfolders, and are left alone. Organizing
never overwrites an existing file at the destination; it errors instead so
you can resolve the collision by hand.

## Acquisition

`prowlarr_url`/`prowlarr_api_key`, `qbittorrent_url`/`qbittorrent_username`/
`qbittorrent_password`, and `sabnzbd_url`/`sabnzbd_api_key` are each
independently optional — leaving any of them blank simply leaves that part
of the Wanted tab unavailable (search reports a plain "not configured"
error if Prowlarr is unset; grabbing a torrent or usenet release does the
same if the matching download client is unset), nothing else in CantiNode
is affected. All three can be set directly in `config.yaml`/env or through
Settings → Acquisition in the web UI.

qBittorrent and SABnzbd are deliberately generic, protocol-typed
connections — not tied to any one server. Point either (or both) at a
genuine standalone qBittorrent/SABnzbd instance, or at
[AcerviNode](https://github.com/obstruct-exit-emit/AcerviNode), which
exposes compatible APIs for both on one host. Real qBittorrent checks
username and password both; a real SABnzbd server has no API to create a
category on the fly, so its `music` category (CantiNode adds every grab
under it) needs to be created by hand once in SABnzbd's own UI — AcerviNode's
shim pre-registers it automatically instead.

Grabbing a release is always a manual action — CantiNode never
auto-downloads a search result. See [Roadmap](../ROADMAP.md) Phase 4 for
why.

## MusicBrainz rate limiting

CantiNode self-throttles to roughly one request per second, per
[MusicBrainz's own policy](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) —
a library with hundreds of unmatched files can take minutes to fully scan
the first time, by design. There's no configuration knob to change this;
it's not CantiNode's rate limit to raise.

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

Everything except `port` can also be changed live through Settings → the
web UI (or `PUT /api/v1/settings`) — `naming_format`,
`min_match_confidence`, and `organize_on_match` take effect immediately,
no restart needed. `port` requires editing `config.yaml` (or the env var)
and restarting.

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

## MusicBrainz rate limiting

CantiNode self-throttles to roughly one request per second, per
[MusicBrainz's own policy](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) —
a library with hundreds of unmatched files can take minutes to fully scan
the first time, by design. There's no configuration knob to change this;
it's not CantiNode's rate limit to raise.

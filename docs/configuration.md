# Configuration

## config.yaml

Created on first run in the data directory, with a generated API key:

```yaml
host: 0.0.0.0
port: 7845
api_key: <generated>
log_level: info        # debug, info, warn, error
auth:                  # present once a login account is added
  users:               # one or more accounts; exactly one is the default
    - username: you
      password_hash: pbkdf2-sha256$...
      default: true    # the protected primary account (cannot be removed)
      role: admin      # admin | member (omitted = admin; the default user is
                       #   always admin). Members can't reach settings/accounts
metadata:
  active: hardcover              # primary book provider: hardcover |
                                 #   openlibrary | googlebooks
  fallbacks: [openlibrary, googlebooks]  # book providers, in order, consulted
                                 #   ONLY when the active one draws a blank on a
                                 #   search or lookup (Settings → Metadata →
                                 #   Fallbacks); omit for none
  manga_provider: anilist        # anilist | hardcover | none (Settings → Metadata)
  comic_provider: hardcover      # hardcover | comicvine | none
  manga_cover_source: provider   # provider | file — manga volume covers
  comic_cover_source: provider   # provider | file — comic issue covers
  language: english              # global metadata preference — providers
  country: united states         #   prefer matching editions, then fall
  include_adult: false           #   back; "none" = no preference
  include_compilations: false    # show box sets / omnibus editions in metadata
                                 #   search (default: hidden, individual books only)
  providers:
    hardcover: { token: "..." }
    comicvine: { token: "..." }
    googlebooks: { token: "..." }  # recommended: keyless shares one global daily
                                   #   quota that's often already spent (HTTP 429);
                                   #   a free key gives you your own. Open Library
                                   #   needs no key.
naming:
  # Each ebook gets its own folder, so sidecars travel with the book.
  ebook_folder: "{Author Name}/{Book Title} ({Release Year})"
  ebook_file: "{Author Name} - {Series Title} {Series Position} - {Book Title} ({Release Year})"
  # audiobook_*, manga_*, comic_*, magazine_* — all editable in the UI
import:                          # Completed Download Handling (Settings →
                                 # Download Clients → Import handling).
                                 # All default to true.
  pack_import_all: true          # multi-book packs fill every matching book,
                                 #   not just monitored ones
  remove_completed: true         # remove the download from the client once
                                 #   imported (torrents too, else they seed)
  delete_completed_files: true   # also delete the downloaded files after
                                 #   import (implies remove_completed)
timings:                         # background cadences — omit for defaults
  search_interval_hours: 6       # wanted sweep (1–168)
  refresh_interval_hours: 720    # metadata re-sync (6–2160; default 30 days)
  health_interval_minutes: 15    # health checks (5–1440)
  import_interval_seconds: 60    # download-client poll (30–3600)
path_mappings:                   # remote client paths → local paths
  - remote: /storage_1           # as the download client reports them
    local: /mnt/media            # where this server sees the same files
```

Environment variables override the file: `LIBRINODE_HOST`, `LIBRINODE_PORT`,
`LIBRINODE_API_KEY`, `LIBRINODE_LOG_LEVEL`, `LIBRINODE_HARDCOVER_TOKEN`.
The data directory itself is chosen with `--data <dir>`.

## Remote path mappings

When a download client runs on another machine or in a container, it reports
paths from *its* filesystem. Without a mapping, LibriNode can only import
those downloads if the share is mounted at the identical path. **Settings →
Download Clients → Remote path mappings** maps a remote prefix to a local
one — the longest matching prefix wins, matching is boundary-aware and
case-insensitive (Windows clients), and separators convert automatically, so
`C:\downloads\Book` maps cleanly onto `/mnt/dl/Book`. Applied to every
client-reported path before import touches disk.

## Background timings

**Settings → General → Advanced: background timings** tunes the four loops
(wanted search, metadata refresh, health checks, import polling). Blank
fields use the defaults; entered values are clamped to the ranges above so a
typo can't hammer your indexers. Changes apply on the next server start.

## Naming templates

Tokens: `{Author Name}`, `{Author SortName}`, `{Book Title}`,
`{Series Title}`, `{Series Position}`, `{Series Position 00}` (zero-padded,
so `Vol. 01` sorts before `Vol. 10`), `{Release Year}`. Tokens without a
value drop out cleanly; emptied fields revert to defaults (a partial save
can never wipe another type's templates). Folder templates may span several
levels with `/` — a level that renders empty drops away, so a year-less book
nests one level shallower (and the magazine default
`{Series Title}/{Release Year}` files issues under per-year subfolders).

## Authentication

Add a user under **Settings → General → Security** to replace the API-key
prompt with a login page (30-day in-memory sessions — restarts sign everyone
out). You can keep several accounts: each row has **change password**, and
non-default users get a role toggle (**promote/demote**), **make default**,
and **remove**. One user is always the protected **default** — it can't be
removed until you promote another user in its place. **Disable login** removes
every account and returns to the API-key prompt. Passwords are stored only as
PBKDF2-SHA256 hashes.

Each account is an **admin** or a **member**. Members get everyday use —
browsing, monitoring, search, grab, scan, organize, and their own password —
but not the server's own configuration (Settings, Indexers, Download Clients,
Quality Profiles, backups, logs, root folders) or other accounts; the backend
refuses those routes, not just the UI. Admins get everything. The default user
is always an admin, so an instance can't be locked out of administration, and
changing a role (or password, or removing a user) revokes that account's other
sessions immediately. Accounts created before roles existed load as admins, so
nothing changes until you deliberately restrict someone. The API key stays
admin-equivalent for Prowlarr and scripts.

A brand-new instance offers a **first-run setup wizard** instead (no API key
needed): it creates the first account — which becomes the default — and walks
through libraries, metadata, an indexer, and a download client.

The API key keeps working for Prowlarr and scripts regardless, and can be
regenerated from the same page. For HTTPS, see the next section.

## HTTPS & reverse proxies

LibriNode itself serves plain HTTP. For access beyond your LAN, put it
behind a TLS-terminating reverse proxy **and enable the login**. Never
expose the raw HTTP port directly to the internet.

Caddy makes it a two-liner (automatic certificates):

```
librinode.example.com {
    reverse_proxy 127.0.0.1:7845
}
```

nginx equivalent:

```nginx
server {
    listen 443 ssl;
    server_name librinode.example.com;
    # ssl_certificate / ssl_certificate_key ...
    location / {
        proxy_pass http://127.0.0.1:7845;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Health checks

Every 15 minutes (and on demand from the System page) LibriNode verifies
root folders are reachable, enabled indexers answer, download clients are
up, and the metadata token is valid — plus warnings when nothing is
configured at all. Issues appear as a banner on every page.

## Logs

`<data>/logs/librinode.log`, size-rotated (5 MB, 3 old files kept). The
System page tails it with a text filter; `log_level: debug` for more.

## Backups

**System → Backups**: a backup is a zip of a consistent database snapshot
plus `config.yaml`, stored under `<data>/backups`. Restore stages the files
and applies them on the next restart, keeping the replaced ones as
`*.pre-restore`. Download the zips somewhere safe.

## Image cache

Two kinds of images are cached under `<data>/covers`, both disposable and
safe to delete (they rebuild on demand):

- **Extracted comic covers** (`covers/book-<id>`): a manga/comic volume's
  cover, pulled from the owned archive's first page and re-extracted when the
  source file changes. Clear it from **Settings → Metadata → Clear extracted
  covers**.
- **Provider art** (`covers/remote/…`): author portraits and series/book
  covers from any provider (Hardcover, AniList, ComicVine, Open Library,
  Google Books), downloaded on add/refresh so the UI serves them locally and
  they survive the provider's link rot.

**Settings → Metadata** has buttons to clear each of these, plus
**Descriptions** (stored in the database — cleared descriptions return on the
next metadata refresh) and **Clear all**, which wipes every rebuildable cache
at once.

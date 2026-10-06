# AI-Generated Playlists — Design Plan

Planning doc only — no code yet. This is a mirror of the live, editable
version at: https://claude.ai/code/artifact/01a636e5-dc0b-426b-81a5-5548d8a6973a
(edit there for inline comments during active iteration; this file gets
updated to match after each real change, so neither copy goes stale).

Not linked from mkdocs nav — this describes a feature that doesn't exist
yet, not current user-facing behavior.

## Goals

Turn a free-text prompt (mood, genre, artist, a specific song — anything)
into a real playlist. Tracks CantiNode already owns go straight in.
Tracks it doesn't own get fetched as single files — not whole albums —
into a temporary holding area that auto-expires after a week of disuse
unless the user keeps one. Keeping promotes exactly that one track into
the real library, permanently. Wanting the whole album later is a
separate, deliberate action.

The overriding design constraint, stated by the user directly: the real
library must never be at risk from this feature's own cleanup logic,
even by accident.

## User flow

1. User opens Playlists → "Generate with AI" → types a prompt.
2. CantiNode asks an LLM to turn the prompt into a concrete track list
   (artist + title, maybe a loose album hint) — a creative/knowledge
   step, not a database query.
3. Each suggested track gets resolved against MusicBrainz and checked
   against the owned library:
   - **Owned** → added to the new playlist immediately, as a normal
     track reference.
   - **Not owned** → CantiNode finds the most-available release
     containing it, fetches *only that track* from it, and lands it in a
     temp holding area. The playlist shows it as "pending" until the
     fetch completes, then as a normal (temp) entry.
4. The playlist exists immediately, mixing owned and temp tracks from
   the start.
5. A temp track that's unused for 7 days and not "kept" gets deleted
   automatically.
6. "Keep" promotes exactly that one track into the real library —
   permanent, safe from deletion, but still just that one song.
7. "Get the whole album" is a separate, explicit action — the existing
   Missing/wanted-album flow, which needs to recognize the already-owned
   single and not duplicate it.

## Architecture

The big win: this doesn't need a parallel system. A temp track is a
normal `track_files`/`tracks` row, scanned and matched by the *existing*
scanner/matcher pipeline, just sitting under a root folder flagged
`is_temp`. That flag is the single safety-load-bearing concept in the
whole design.

| Piece | What it is |
| --- | --- |
| `internal/aiplaylist` (new) | Prompt → LLM call → structured track list → per-track resolution, reusing `internal/musicbrainz` search, `internal/candidatesearch`/`internal/release` for picking a release, `internal/download` + `internal/importer`-style single-file fetch |
| Root folders | Gain an `is_temp` flag. One (or more) root folder is designated as temp storage. The Library view simply never lists root folders where `is_temp = true` — this is the core safety mechanism (see Safety design) |
| `track_files` / `albums` | Gain `temp_expires_at` / `last_referenced_at` columns. A temp file is otherwise an ordinary row |
| New background loop | Expires temp tracks, `runBG`-wired like every other periodic sweep, scoped *by query* to `is_temp` root folders only |
| Promote ("keep") | The existing organize/move machinery, re-parenting the file into a real root folder and clearing every temp flag — not just a boolean flip |
| `playlists` table | Gains `source` / `prompt` columns (the base table already exists — migration 034) |

## Reliability & concurrency

- **Failed fetches retry the next candidate automatically** — the same
  pattern autosearch already uses when a release turns out to be dead
  (confirmed live during tonight's burn-in: two dead torrents in a row
  for one album before the third succeeded). A single-track fetch
  should never just silently leave a playlist slot empty after one bad
  torrent.
- **Concurrent double-fetch guard**: two playlists (or a regenerate)
  wanting the same not-yet-owned track at the same time must not fetch
  it twice. Needs the same compare-and-swap claim shape
  `ClaimWantedAlbumForDownload`/`ClaimAlbumForUpgrade` already give
  wanted/upgrade albums, scoped to "this recording is already being
  fetched for the temp library."
- **"Most available album" needs a tie-break rule, not just a pick.** A
  popular song often exists on the original studio album *and* a
  compilation *and* a live album. Prefer the canonical studio release
  the same way `pickRepresentativeRelease` already does (Official
  status, earliest/most standard edition), falling back to anything
  else only when no studio release is found.
- **Decided:** playlist generation runs as a background operation,
  decoupled from the request/browser tab — the same principle already
  established for grabs/scans/organizes (ROADMAP's "Background-operation
  audit").
- **Decided:** the double-fetch claim is global across users, not
  per-user — two different users' playlists wanting the same missing
  song share one fetch.
- **Decided:** the LLM's own output only needs artist/album/song per
  suggested track — no description, genre, mood, or other metadata from
  the LLM itself, to save time/cost. Resolution against MusicBrainz
  still fails closed: a suggestion that doesn't cleanly resolve is left
  "not found" in the preview rather than forcing a weak match.

## Single-track fetch mechanics

Grabbing "just one track" is easy to say, harder to execute per protocol:

- **Torrent (qBittorrent):** supports per-file priority — can download
  only the target file from a multi-file torrent. Real bandwidth/storage
  savings.
- **Usenet (SABnzbd):** no clean per-file selection; realistically the
  whole post downloads, and CantiNode discards everything but the target
  file afterward.

**Proposal:** prefer torrent sources for single-track fetches when
available; fall back to usenet with post-download discard. This is an
open decision — see Open questions.

## Safety design

This is the part that matters most. The core worry — accidental deletion
from the real library — gets addressed structurally, not just by
convention:

- The expiry sweep's query is **scoped to `is_temp` root folders only**.
  It cannot see, let alone delete, a file under a real root folder — not
  "won't," *can't*, by construction of the query itself.
- Promotion is a real move: file relocated, row re-parented to a real
  root folder, temp flags cleared. Nothing stays dual-tagged.
- The temp view is a separate page/section, never merged into the main
  Library grid, so there's no shared UI surface where a misclick could
  hit a real-library delete action meant for temp cleanup (or vice
  versa).
- Open question: a short dry-run log ("these N tracks will be deleted on
  the next sweep") or even a brief undo grace period before actual
  deletion — cheap to add, worth deciding now rather than retrofitting.

## Temp lifecycle

Proposed as the literal rule (needs the user's confirmation it matches
what they meant): a temp track's clock is "time since it was last part
of *any* playlist," not a flat one-shot week from creation.

- `last_referenced_at` updates whenever a playlist containing the track
  is saved, or still exists at sweep time.
- The sweep deletes a temp track only if: not kept, AND not currently in
  any playlist, AND `now − last_referenced_at > 7 days`.
- Removing a track from its only playlist starts the clock; adding it to
  another playlist resets it.
- A count/size cap on the temp area, independent of the 7-day timer —
  otherwise generating several playlists in a short window has no
  backstop besides time.
- **Decided:** an expired (deleted) temp track's playlist entry stays
  visible as "expired" rather than vanishing, with a "Reactivate" action
  that re-fetches it the same way as a fresh miss.

## UI placement

- "Generate with AI" lives inside the Playlists section, as the user
  asked — a panel/modal for the prompt, a preview of the resolved track
  list (owned / fetching / not found) before committing.
- The temp library gets its own distinct view — not nested in Library —
  maybe reachable only from the AI-playlist flow itself (a "view temp
  holding area" link) rather than main nav, so it's there when relevant
  and invisible otherwise.

## Open questions

- [ ] **LLM choice**: Claude API (needs the user's own API key, same
  BYOK pattern as the AudioDB/Last.fm settings) — fine
  configuring/paying for that, or something keyless preferred?
- [ ] **Per-file selective download**: torrent-only for v1 (simpler,
  real savings) with usenet as a slower fallback — OK, or should usenet
  be unsupported for this feature initially?
- [ ] Does the Temp lifecycle rule above match what the user meant, or
  did they picture something simpler (flat 7 days from fetch, full
  stop)? — **asked as a live comment on the doc, awaiting reply.**
- [ ] Roughly how many tracks per generated playlist — fixed count, or
  let the LLM decide per prompt?
- [ ] Dry-run/undo window before real deletion — worth it, or is "7 days
  plus a Keep button" enough safety margin on its own?
- [ ] Does regenerating/editing a prompt revise the existing playlist,
  or create a new one?
- [ ] Does "keep" (a permanent real-library write) need the same
  admin-only gate CantiNode's other real-library mutations already
  have, for a non-admin member account?
- [x] ~~Plex sync interaction~~ — explicitly deferred, out of scope for
  v1. Revisit once the core feature is built.

## Status / changelog

- 2026-10-06: First draft complete in both the live doc and this mirror.
  All sections above are initial proposals, not decisions — every one
  is open to revision as we keep building this out. Left one open
  comment on the doc re: the temp-lifecycle rule.
- 2026-10-06: Added a "Reliability & concurrency" section (retry-next-
  candidate on fetch failure, a claim to prevent double-fetching the
  same track, and a tie-break rule for picking among multiple albums
  containing the same song) plus two more open questions (regenerate
  behavior, keep's permission level) and a temp-area size/count cap.
- 2026-10-06: Five more decisions: Plex sync interaction deferred (not
  v1 scope); expired temp tracks stay visible with a Reactivate action;
  generation runs as a background operation; the double-fetch claim is
  global across users; the LLM's output is limited to artist/album/song
  (no description/genre/mood) and resolution fails closed.

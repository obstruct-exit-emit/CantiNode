# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

CantiNode is a self-hosted music library automation server (an *arr-style
alternative to Lidarr): monitors artists, searches indexers via a native
Prowlarr connection (or plain Newznab/Torznab), grabs releases, and scans/
matches files against MusicBrainz into an organized, tagged library. Single
self-contained Go binary with an embedded React frontend, SQLite (pure Go,
no cgo), served on port 7847. Pre-1.0, feature-complete — see
[ROADMAP.md](ROADMAP.md) for what's left and [CHANGELOG.md](CHANGELOG.md)
for recent work; don't re-derive that history here, read those files.

Full docs: [docs/index.md](docs/index.md) (start there for user-facing
behavior — libraries, acquisition, configuration, the full REST API).
[docs/development.md](docs/development.md) has the authoritative
package-by-package layout table; this file focuses on cross-cutting
architecture that table doesn't show.

## Commands

```sh
go run ./cmd/cantinode        # start on http://localhost:7847
go build ./cmd/cantinode      # embeds web/dist if present at build time
go vet ./...                  # the project's only "lint" — CI runs exactly this + test + build, nothing else
go test ./...                 # full suite
go test ./internal/musicscanner/... -run TestGroupMultiDiscFolders -v   # one package / one test
```

Frontend (Node 22+, in `web/`):

```sh
npm install
npm run dev      # Vite dev server, proxies /api to :7847
npm run build    # tsc -b (typecheck) && vite build -> web/dist; go:embed bakes in whatever's on disk at Go build time, not what's in git
```

There is no separate frontend lint/test command — `npm run build`'s `tsc -b`
is the only frontend check that exists.

**Always develop through WSL here — user preference, do all of it there,
not just when native Windows happens not to work.** From a Windows shell:
`wsl -e bash -lc "export PATH=/usr/local/go/bin:$PATH; cd /mnt/c/Code/CantiNode && <command>"`
for Go (node/npm are natively on WSL's own PATH already, no export
needed). Official builds are Linux-only for now anyway (Docker/Windows
builds on hold — see ROADMAP.md), and the live test instance below runs
there. (Native Windows `go`/`node`/`npm` are actually on PATH and do work
here if ever genuinely needed — verified directly, not assumed — but don't
default to it: WSL is the standing instruction. One nuance if native ever
does come up: `npm install`'s bin shims, e.g. `tsc`, aren't portable
across the two, so `npm run build`/`dev` has to run from whichever side —
Windows or WSL — `npm install` itself ran from, since `web/node_modules`
is one shared directory on disk either way.)

A live WSL systemd service (`cantinode.service`, data dir
`/var/lib/cantinode`, same port 7847) exists in this same WSL distro for
manual/live verification. **It's deliberately `disabled`, not
`enabled`** (user preference, set 2026-09-07) — it does NOT auto-start
when WSL boots, only when explicitly started. Never assume it's up:
`sudo systemctl status cantinode` first. Turn it on/off with `sudo
systemctl start cantinode` / `stop cantinode` — don't `enable` it (that
would silently restore auto-start on boot, undoing this). Redeploying a
locally-built fix to it: `sudo systemctl stop cantinode`, replace
`/usr/local/bin/cantinode`, `sudo systemctl start cantinode`. Login/
API-key details for that instance aren't in this repo — don't invent or
assume any.

## Architecture

Two pipelines connect most of the packages `docs/development.md` lists
individually; understanding the handoffs between them matters more than any
one package in isolation.

**Library scan/match** (`internal/musicscanner`): `ScanRootFolder` walks a
root folder, reads each new file's tags (`internal/tagreader`), and groups
still-unmatched files by directory. `groupMultiDiscFolders`
(`folder_match.go`) then merges CD1/CD2/Disc-N sibling folders of the same
album into one group *before* matching — purely in-memory, files never move
on disk until an explicit Organize. `matchFolder` resolves one MusicBrainz
release for the whole merged group (`resolveFolderRelease`: embedded
release MBID, then grab-provenance fast path via
`ExpectedReleaseGroupMBID`, then a real MusicBrainz release search) and
slots each file into a track (`matchEntriesToRelease`/`slotTrack`), falling
back to independent per-file fuzzy search only when a group can't be
resolved with confidence. Within the grab-provenance fast path,
`pickBestVersionByFileCount` picks the cached version closest to the
group's own file count by `|TrackCount - fileCount|` alone — when two or
more cached versions land on the exact same count (confirmed live: a
Hozier "Deluxe Edition" with a BBC-covers bonus disc and a genuinely
different "Deluxe Edition" with real bonus tracks, both cached at 17
tracks), that distance metric can't tell them apart at all, so
`resolveVersionTieByTitles` fetches each exact-tied candidate's own real
tracklist and picks whichever one's titles actually match the local
files — reusing whichever fetch wins rather than re-fetching it, and
never triggered outside that specific exact-count-tie case, so the
ordinary single-best-by-count pick pays no extra MusicBrainz round trips.
**Non-obvious gotcha**: a per-disc Album-tag
suffix ("Album CD 1" vs "Album CD 2" — genuinely common) has to be stripped
consistently everywhere a merged group's tags get compared —
`folderTagConsensus` and `albumTagsDisagree` both do this now — or the
search step silently disagrees with the merge decision that already
happened and degrades to much weaker per-file matching. A lone disc-pattern
folder (a sibling already matched/owned from an earlier scan, so it's
invisible to this scan's own grouping) still gets a real release search
rather than being treated like an ordinary standalone file — see
`resolveFolderRelease`'s own comment on `discFolderPattern`. **Another
non-obvious gotcha, in `internal/musicbrainz`**: a whitespace-padded
subtitle separator (" - ", " : ", an en/em-dash) anywhere in a release
title fed to `SearchReleases`/`SearchRecordings` makes MusicBrainz's own
phrase-query search return *zero* results, even when every real word is
present and correct — `sanitizeReleaseTitle` collapses one to a plain
space before searching now, but a fresh symptom that looks like "the
whole-folder search silently found nothing" is worth checking against
this before assuming it's a `folderTagConsensus`/grouping problem again;
confirm with a direct `SearchReleases` call before touching the matching
code itself. A colon/dash glued straight to a word (real title
punctuation, or a hyphenated word) is deliberately left alone.

**A third gotcha, this one structural**: the automatic scanner above and
the manual Unmatched Files "Auto-match" review flow (`SuggestMatches`,
same file) are two genuinely separate matching code paths that don't
automatically share fixes — `SuggestMatches` calls `slotTrack` directly
on a file's raw cached tags, never through `groupMultiDiscFolders`, so a
fix to the automatic scanner's own disc handling doesn't reach the manual
flow unless applied there too (confirmed live: `SuggestMatches` now also
infers a missing disc number from the file's own CD1/CD2 folder name,
same as the scanner already did). Relatedly, the manual flow's own
Version dropdown reads from `release_group_versions`, a cache that — once
populated, ever — is normally trusted forever; `GET .../versions` takes
an optional `?minTracks=N` specifically so the unmatched-files page can
force a fresh look when nothing cached is a plausible match for its own
known file count, rather than silently offering a stale, wrong-track-count
edition as the default pick.

**A fourth gotcha**: `slotTrack`'s disc+track-number fast path used to
trust a file's embedded `TrackNumber` tag alone, with no title check at
all, on the reasoning that a ripper/tagger's own numbering is normally
reliable — confirmed live against a fan-compiled discography torrent that
happened to bundle two independently-sourced copies of the same album: a
file in the second copy had a correct-looking track number but an
embedded title for a *completely different song*, and the fast path
slotted it into a real track's position anyway, with high confidence,
leaving that album with two different tracks both claiming the same
position. `slotTrack` now also requires the title to at least clear
`trackNumberSanityThreshold` (`folder_match.go`) — deliberately far looser
than the real title-match threshold below it, since this is a sanity
check against a flagrant mismatch, not a second title match. A plain
`relname.TitleSimilarity` ratio alone isn't enough for that sanity check,
though: a file correctly missing an edition/version qualifier ("Spectres"
vs "Spectres (Instrumental Version)") scores almost identically low to a
real mismatch purely by string-length coincidence (confirmed live: 0.276
vs 0.278) — `relname.TitleIsPrefixOf` catches that specific "same song,
qualifier only on one side" case as a second, independent signal, checked
alongside the ratio rather than instead of it.

**A fifth gotcha**: `slotTrack` only ever read a file's *embedded*
`TrackNumber`/`Title` tags, with no fallback to the filename itself —
confirmed live against a real, completely tagless release (every file's
Artist/Album/Title/TrackNumber all blank, relying entirely on
conventionally numbered filenames like `"11 - 40'.flac"`), which left
every file unmatched even once the whole folder's own release was
confidently resolved via grab provenance. `filenameTrackFallback`
(`pathfallback.go`) extracts a track-number/title guess from the filename
and feeds it through `slotTrack`'s own unchanged sanity check, so a
filename-derived guess gets exactly the same flagrant-mismatch protection
an embedded tag already does. Same structural gotcha as the third one
above: `suggest.go`'s `SuggestMatches` calls `slotTrack` independently of
the automatic scanner's own call in `folder_match.go`, so this had to be
applied in both places.

**Acquisition** (`internal/candidatesearch` → `internal/download` →
`internal/importer`): a search (manual, or `internal/autosearch`'s
periodic wanted-list sweep) fans out through `internal/indexer` (including
the native `prowlarr` source), scores results (`internal/release`), and a
grab hands off to a qBittorrent/SABnzbd/direct client
(`internal/download`). `internal/importer.PollOnce` (its own 2-minute
timer, or the "Import now" button) polls in-flight grabs, and once one
completes, copies its audio files into a root folder and matches them in
via `Scanner.ScanFolder`, scoped to exactly that destination directory —
deliberately not the library-wide `ScanAll` above, which used to run here
and re-examine every already-matched file in the whole library on every
single import (confirmed live: ~190ms/file on real, likely
network-mounted storage — minutes of pure overhead unrelated to what was
actually new). But first,
`looksLikeSingleFileWholeAlbumRip` rejects (deletes + blocklists +
reverts to wanted) a completed download that copied exactly one audio
file for a release group whose cached versions (`ListReleaseGroupVersions`,
local, no MusicBrainz call) confidently show more than one track — some
rips pack a whole album into one continuous file meant to be split via an
accompanying `.m3u`/`.cue`, which can never be matched/organized and,
worse, could get wrongly slotted into a single track position if the scan
were allowed to try. An **upgrade** grab
(`GrabRecord.UpgradeAlbumID`, not `WantedAlbumID` — the album is already
owned) additionally runs `swapUpgradedFiles` afterward: deletes the old
file for each track actually superseded, track-by-track, never wiping
anything the new release didn't end up matching. Starting the grab itself
claims the album first (`ClaimAlbumForUpgrade`/`albums.upgrade_pending`,
the same compare-and-swap shape `ClaimWantedAlbumForDownload` gives a
wanted album) — a second concurrent upgrade request for the same album
gets a clean 409 rather than reaching `GrabRelease` at all, which matters
because two independent `GrabRecord`s for the same album would otherwise
race each other's `swapUpgradedFiles` "before" snapshot. `PollOnce`
releases the claim once the grab resolves either way. Matches primarily by
MusicBrainz recording ID (`musiclibrary.GetOrCreateTrack` is keyed by
`(albumID, recording MBID)`), with a same-disc/track-position +
title-similarity fallback (`swapByPosition`, using
`internal/relname.TitleSimilarity`) for a remaster MusicBrainz assigns a
fresh recording ID to despite it being the same song — deliberately never
triggered by position alone, to avoid pairing (and deleting) the wrong file
across a reordered tracklist.

Both pipelines are explicitly "never worse than not matching" in their
fallback design: an unconfident signal degrades to a weaker match attempt
or leaves a file in the Unmatched review queue, never guesses and locks in
a wrong artist/album/track.

**Background loops** are all wired in `cmd/cantinode/main.go` — that's the
authoritative list of what actually runs on a timer versus what's
manual-trigger-only (e.g. quality-profile upgrades are deliberately
manual-only, not swept; see `internal/importer`'s doc comment and
`docs/acquisition.md`). Each is launched through `runBG`, not a bare `go`
statement: it joins a `sync.WaitGroup` that shutdown waits on (with a grace
period) before the process exits, so an in-flight pass — e.g. the importer
mid-copy of a finished grab's files — gets a real chance to finish rather
than racing process exit; and it recovers a panic anywhere in that loop's
own call chain, logging and restarting the loop rather than taking down the
whole process (including the HTTP server and every *other* loop) over one
bad input. A new loop should go through `runBG` too, not `go` directly.

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
resolved with confidence. **Non-obvious gotcha**: a per-disc Album-tag
suffix ("Album CD 1" vs "Album CD 2" — genuinely common) has to be stripped
consistently everywhere a merged group's tags get compared —
`folderTagConsensus` and `albumTagsDisagree` both do this now — or the
search step silently disagrees with the merge decision that already
happened and degrades to much weaker per-file matching. A lone disc-pattern
folder (a sibling already matched/owned from an earlier scan, so it's
invisible to this scan's own grouping) still gets a real release search
rather than being treated like an ordinary standalone file — see
`resolveFolderRelease`'s own comment on `discFolderPattern`.

**Acquisition** (`internal/candidatesearch` → `internal/download` →
`internal/importer`): a search (manual, or `internal/autosearch`'s
periodic wanted-list sweep) fans out through `internal/indexer` (including
the native `prowlarr` source), scores results (`internal/release`), and a
grab hands off to a qBittorrent/SABnzbd/direct client
(`internal/download`). `internal/importer.PollOnce` (its own 2-minute
timer, or the "Import now" button) polls in-flight grabs, and once one
completes, copies its audio files into a root folder and runs the same
scanner scan above to match them in. An **upgrade** grab
(`GrabRecord.UpgradeAlbumID`, not `WantedAlbumID` — the album is already
owned) additionally runs `swapUpgradedFiles` afterward: deletes the old
file for each track actually superseded, track-by-track, never wiping
anything the new release didn't end up matching. Matches primarily by
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
`docs/acquisition.md`).

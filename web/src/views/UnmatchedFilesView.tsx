import { useCallback, useEffect, useState } from "react";
import {
  api,
  type MusicArtist,
  type MusicBrainzRecordingResult,
  type MusicReleaseGroup,
  type MusicTrackFile,
  type ReleaseGroupVersion,
  type TrackSuggestion,
  type UnmatchedTrackFile,
  type WantedAlbum,
} from "../api";
import { RowsSkeleton } from "../components/Skeleton";
import { formatBytes, formatDuration } from "../format";

// normalizeForMatch/bigrams/diceSimilarity are a small, dependency-free
// fuzzy string match (Sørensen–Dice coefficient over character bigrams) —
// good enough to rank a folder's own tag-consensus artist/album against a
// short list of real library entries for the "Auto-match" button, without
// pulling in a fuzzy-matching library just for this.
function normalizeForMatch(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
}

function bigrams(s: string): string[] {
  const out: string[] = [];
  for (let i = 0; i < s.length - 1; i++) out.push(s.slice(i, i + 2));
  return out;
}

function diceSimilarity(a: string, b: string): number {
  const na = normalizeForMatch(a);
  const nb = normalizeForMatch(b);
  if (!na || !nb) return 0;
  if (na === nb) return 1;
  const ba = bigrams(na);
  const bb = bigrams(nb);
  if (ba.length === 0 || bb.length === 0) return 0;
  const counts = new Map<string, number>();
  for (const g of ba) counts.set(g, (counts.get(g) ?? 0) + 1);
  let matches = 0;
  for (const g of bb) {
    const c = counts.get(g) ?? 0;
    if (c > 0) {
      matches++;
      counts.set(g, c - 1);
    }
  }
  return (2 * matches) / (ba.length + bb.length);
}

// discSuffixPattern/stripDiscSuffix mirror internal/musicscanner's own
// Go-side helpers of the same name (folder_match.go) — a per-disc file's
// own Album tag very often carries a disc-number qualifier ("Moonglow CD
// 1", "Moonglow CD 2") that the real library album title never does,
// which both splits tagConsensus's vote count across two different
// strings for what's genuinely one album AND makes the subsequent
// fuzzy-match against real album titles score too low to clear the
// confidence bar. Stripped before either happens.
const discSuffixPattern = /[\s([-]+(?:cd|disc|disk|d)[\s._-]*0*[0-9]+\)?\s*$/i;

function stripDiscSuffix(album: string): string {
  return album.replace(discSuffixPattern, "").trim();
}

// tagConsensus picks the most common non-empty Artist/Album tag across a
// folder's files — a looser, JS-side echo of the Go scanner's own strict
// folderTagConsensus (internal/musicscanner/folder_match.go), good enough
// to seed an auto-match guess even when a few files disagree, since a
// wrong guess here just fails the confidence threshold rather than
// committing anything.
function tagConsensus(files: UnmatchedTrackFile[]): { artist: string; album: string } {
  const artistCounts = new Map<string, number>();
  const albumCounts = new Map<string, number>();
  for (const f of files) {
    const tags = parseTags(f.tagsJson);
    const artist = (tags.AlbumArtist || tags.Artist || "").trim();
    const album = stripDiscSuffix((tags.Album || "").trim());
    if (artist) artistCounts.set(artist, (artistCounts.get(artist) ?? 0) + 1);
    if (album) albumCounts.set(album, (albumCounts.get(album) ?? 0) + 1);
  }
  const mostCommon = (counts: Map<string, number>): string => {
    let best = "";
    let bestN = 0;
    for (const [k, n] of counts) {
      if (n > bestN) {
        best = k;
        bestN = n;
      }
    }
    return best;
  };
  return { artist: mostCommon(artistCounts), album: mostCommon(albumCounts) };
}

// pickBestVersionByFileCount scores each cached version by how close its
// own track count is to fileCount — the folder's own, already multi-disc-
// merged file count (see UnmatchedTrackFile.groupKey) — closest wins, ties
// broken toward the representative version. Returns null when no version
// has a usable track count at all.
function pickBestVersionByFileCount(
  versions: ReleaseGroupVersion[],
  fileCount: number,
): ReleaseGroupVersion | null {
  let best: ReleaseGroupVersion | null = null;
  let bestDiff = Infinity;
  for (const v of versions) {
    if (v.trackCount <= 0) continue;
    const diff = Math.abs(v.trackCount - fileCount);
    if (diff < bestDiff || (diff === bestDiff && v.isRepresentative && !best?.isRepresentative)) {
      best = v;
      bestDiff = diff;
    }
  }
  return best;
}

// FileTags is the best-effort subset of internal/tagreader.Tags a file's
// own embedded metadata carried — never authoritative (that's the whole
// reason the file ended up here unmatched), but useful as a head start:
// pre-filling the search form beats making someone retype what the file
// already told the scanner.
interface FileTags {
  Title?: string;
  Artist?: string;
  AlbumArtist?: string;
  Album?: string;
}

function parseTags(json: string): FileTags {
  try {
    return JSON.parse(json) as FileTags;
  } catch {
    return {};
  }
}

// splitPath separates a file's own name from its containing directory —
// paths reflect the server's own filesystem (already translated through
// any remote path mapping), so either separator can show up.
function splitPath(path: string): { dir: string; name: string } {
  const idx = Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
  return idx >= 0 ? { dir: path.slice(0, idx), name: path.slice(idx + 1) } : { dir: "", name: path };
}

// UnmatchedFilesView is the dedicated review queue for scanned files the
// matcher couldn't confidently place — split out from the Music library
// page into its own sidebar destination once it stopped being a short
// afterthought list. Grouped by containing folder (a botched import's
// files usually land in one folder together, so reviewing them as a
// batch beats one long undifferentiated list); each group can either be
// reviewed file-by-file (search form pre-filled from the file's own tags)
// or auto-matched as a batch against one of the artist's own wanted/missing
// albums (see AutoMatchPanel) — either way nothing commits without an
// explicit approval.
export default function UnmatchedFilesView({ onError }: { onError: (message: string) => void }) {
  const [files, setFiles] = useState<UnmatchedTrackFile[] | null>(null);
  const [artists, setArtists] = useState<MusicArtist[]>([]);
  const [autoMatchConfidence, setAutoMatchConfidence] = useState(0.85);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [filter, setFilter] = useState("");
  const [autoMatchDir, setAutoMatchDir] = useState<string | null>(null);
  // Files with a pending (not-yet-approved) auto-match suggestion shouldn't
  // also show in the plain file list below — that just doubles up the same
  // file with two different action buttons. Only ever populated for
  // whichever single group has its auto-match panel open.
  const [pendingMatchIds, setPendingMatchIds] = useState<Set<number>>(new Set());

  const reload = useCallback(() => {
    api
      .listUnmatchedTrackFiles()
      .then(setFiles)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);
  useEffect(() => {
    api.listMusicArtists().then(setArtists).catch(() => {}); // the auto-match dropdown just starts empty on failure
    api
      .getMusicSettings()
      .then((s) => setAutoMatchConfidence(s.autoMatchConfidence))
      .catch(() => {}); // falls back to the built-in default on failure
  }, []);

  if (!files) return <RowsSkeleton />;

  const remove = (id: number) => {
    setBusyId(id);
    api
      .deleteTrackFile(id)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyId(null));
  };

  const q = filter.trim().toLowerCase();
  const filtered = q
    ? files.filter((f) => f.path.toLowerCase().includes(q) || f.tagsJson.toLowerCase().includes(q))
    : files;

  // Grouped by the server-computed groupKey, not the file's own immediate
  // parent directory — normally the same thing, but for a multi-disc album
  // (CD1/CD2/... subfolders) groupKey is their shared parent, so both
  // subfolders' files land in one group here, matching what the automatic
  // scanner itself treats as "one album's files" (see
  // internal/musicscanner's groupMultiDiscFolders).
  const groups = new Map<string, UnmatchedTrackFile[]>();
  for (const f of filtered) {
    const key = f.groupKey || splitPath(f.path).dir;
    const list = groups.get(key);
    if (list) list.push(f);
    else groups.set(key, [f]);
  }
  const sortedDirs = [...groups.keys()].sort();

  return (
    <section className="card">
      <div className="card-head">
        <h2>Unmatched files ({files.length})</h2>
      </div>
      <p className="muted">
        Scanned but not confidently matched to a MusicBrainz recording.
        Auto-match a whole folder against one of the artist's own wanted or
        missing albums, or search and match a file by hand below — either
        way, review the proposal before it's applied.
      </p>

      {files.length === 0 ? (
        <div className="empty-state">
          <span className="empty-icon" aria-hidden="true">
            ✅
          </span>
          <h3>Nothing to review</h3>
          <p className="muted">Every scanned file matched cleanly.</p>
        </div>
      ) : (
        <>
          {files.length > 10 && (
            <input
              className="grid-filter"
              placeholder="Filter by path or tag…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          )}
          {filtered.length === 0 && <p className="muted">No files match the filter.</p>}
          {sortedDirs.map((dir) => {
            const groupFiles = groups.get(dir)!;
            return (
              <div key={dir}>
                <div className="card-head">
                  {sortedDirs.length > 1 ? (
                    <h3 className="group-heading">
                      {dir || "(root)"} ({groupFiles.length})
                    </h3>
                  ) : (
                    <span />
                  )}
                  <button
                    className={autoMatchDir === dir ? "toggle on" : "toggle"}
                    onClick={() => setAutoMatchDir(autoMatchDir === dir ? null : dir)}
                  >
                    {autoMatchDir === dir ? "Close auto-match" : "Auto-match…"}
                  </button>
                </div>
                {autoMatchDir === dir && (
                  <AutoMatchPanel
                    files={groupFiles}
                    artists={artists}
                    autoMatchConfidence={autoMatchConfidence}
                    onApplied={reload}
                    onError={onError}
                    onPendingChange={setPendingMatchIds}
                  />
                )}
                <ul className="rows">
                  {groupFiles
                    .filter((f) => !pendingMatchIds.has(f.id))
                    .map((f) => (
                    <li key={f.id}>
                      <UnmatchedFileRow
                        file={f}
                        busy={busyId === f.id}
                        onBusy={() => setBusyId(f.id)}
                        onDone={reload}
                        onRemove={() => remove(f.id)}
                        onError={onError}
                      />
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
        </>
      )}
    </section>
  );
}

// AlbumOption is one entry in the album dropdown — a wanted or missing
// release group from the picked artist's own cached discography.
interface AlbumOption {
  mbid: string;
  title: string;
  sub: string;
}

// AutoMatchPanel proposes track slots for a whole batch of unmatched files
// (normally one folder, or one multi-disc album's worth of CD1/CD2/...
// subfolders merged together — see groupKey) against a release the user
// picks from their own artist's wanted/missing albums — cascading
// dropdowns (album only enabled once an artist is chosen, version only
// once an album is chosen) rather than a fresh MusicBrainz search, since
// the point is reusing what's already in the library. The "Auto-match"
// button pre-fills all three dropdowns itself when it's confident enough
// (see autoMatch) — artist and album by fuzzy name match against the
// folder's own tag consensus, version by which cached edition's track
// count is closest to this folder's own file count — but never applies
// anything on its own: "Suggest matches" only previews a proposal, and
// each suggestion still needs its own "Approve" click (or "Approve all",
// once the proposal itself has been reviewed).
function AutoMatchPanel({
  files,
  artists,
  autoMatchConfidence,
  onApplied,
  onError,
  onPendingChange,
}: {
  files: UnmatchedTrackFile[];
  artists: MusicArtist[];
  autoMatchConfidence: number;
  onApplied: () => void;
  onError: (message: string) => void;
  onPendingChange: (ids: Set<number>) => void;
}) {
  const [artistId, setArtistId] = useState<number | "">("");
  const [albums, setAlbums] = useState<AlbumOption[] | null>(null);
  const [albumMbid, setAlbumMbid] = useState("");
  const [versions, setVersions] = useState<ReleaseGroupVersion[] | null>(null);
  const [releaseMbid, setReleaseMbid] = useState("");
  const [releaseTitle, setReleaseTitle] = useState("");
  const [suggestions, setSuggestions] = useState<TrackSuggestion[] | null>(null);
  const [suggesting, setSuggesting] = useState(false);
  const [autoMatching, setAutoMatching] = useState(false);
  const [applyingId, setApplyingId] = useState<number | null>(null);
  const [approvedIds, setApprovedIds] = useState<Set<number>>(new Set());

  // fetchAlbumsForArtist loads id's own wanted+missing albums into the
  // Album dropdown — shared by the plain artist picker and autoMatch,
  // which both need the same combined, sorted list (autoMatch additionally
  // needs it back as a value to fuzzy-match against, not just as a
  // side-effecting state update).
  const fetchAlbumsForArtist = (id: number): Promise<AlbumOption[]> =>
    Promise.all([api.listWantedMusicAlbums(id), api.listMissingMusicReleaseGroups(id)]).then(
      ([wanted, missing]) => {
        const combined = [
          ...wanted.map((w: WantedAlbum) => ({
            mbid: w.releaseGroupMbid,
            title: w.title,
            sub: `wanted${w.releaseDate ? " · " + w.releaseDate.slice(0, 4) : ""}`,
          })),
          ...missing.map((m: MusicReleaseGroup) => ({
            mbid: m.releaseGroupMbid,
            title: m.title,
            sub: `missing${m.firstReleaseDate ? " · " + m.firstReleaseDate.slice(0, 4) : ""}`,
          })),
        ].sort((a, b) => a.title.localeCompare(b.title));
        setAlbums(combined);
        return combined;
      },
    );

  // fetchVersionsForAlbum loads mbid's cached release versions/editions
  // into the Version dropdown — shared by the plain album picker (which
  // also auto-picks the file-count-closest version as a starting default,
  // still freely changeable) and autoMatch.
  const fetchVersionsForAlbum = (mbid: string): Promise<ReleaseGroupVersion[]> =>
    api.listReleaseGroupVersions(mbid).then((vs) => {
      setVersions(vs);
      return vs;
    });

  const pickArtist = (id: number | "") => {
    setArtistId(id);
    setAlbums(null);
    setAlbumMbid("");
    setVersions(null);
    setReleaseMbid("");
    setSuggestions(null);
    if (id === "") return;
    fetchAlbumsForArtist(id).catch((err: unknown) =>
      onError(String(err instanceof Error ? err.message : err)),
    );
  };

  const pickAlbum = (mbid: string) => {
    setAlbumMbid(mbid);
    setVersions(null);
    setReleaseMbid("");
    setSuggestions(null);
    if (!mbid) return;
    fetchVersionsForAlbum(mbid)
      .then((vs) => {
        const best = pickBestVersionByFileCount(vs, files.length);
        if (best) setReleaseMbid(best.releaseMbid);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  // autoMatch fills in the artist/album/version dropdowns itself, each
  // relying on the field before it to narrow the search — artist and
  // album by fuzzy name match (diceSimilarity) against this folder's own
  // tag consensus, gated at autoMatchConfidence (below that, the dropdown
  // is simply left for the user to pick by hand, same as today); version
  // by file-count closeness against files.length (already multi-disc-
  // merged — see groupKey), which needs no confidence gate of its own
  // since it's just a helpful default, always freely changeable before
  // "Suggest matches" is ever clicked, let alone before any individual
  // suggestion is approved.
  const autoMatch = async () => {
    const { artist: tagArtist, album: tagAlbum } = tagConsensus(files);
    if (!tagArtist) return;

    let bestArtist: MusicArtist | null = null;
    let bestArtistScore = 0;
    for (const a of artists) {
      const score = diceSimilarity(tagArtist, a.name);
      if (score > bestArtistScore) {
        bestArtistScore = score;
        bestArtist = a;
      }
    }
    if (!bestArtist || bestArtistScore < autoMatchConfidence) return;

    setAutoMatching(true);
    try {
      setArtistId(bestArtist.id);
      setAlbumMbid("");
      setVersions(null);
      setReleaseMbid("");
      setSuggestions(null);
      const combined = await fetchAlbumsForArtist(bestArtist.id);
      if (!tagAlbum) return;

      let bestAlbum: AlbumOption | null = null;
      let bestAlbumScore = 0;
      for (const al of combined) {
        const score = diceSimilarity(tagAlbum, al.title);
        if (score > bestAlbumScore) {
          bestAlbumScore = score;
          bestAlbum = al;
        }
      }
      if (!bestAlbum || bestAlbumScore < autoMatchConfidence) return;

      setAlbumMbid(bestAlbum.mbid);
      const vs = await fetchVersionsForAlbum(bestAlbum.mbid);
      const bestVersion = pickBestVersionByFileCount(vs, files.length);
      if (bestVersion) setReleaseMbid(bestVersion.releaseMbid);
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setAutoMatching(false);
    }
  };

  const suggest = () => {
    if (!albumMbid) return;
    setSuggesting(true);
    setSuggestions(null);
    setApprovedIds(new Set());
    api
      .suggestTrackFileMatches(
        files.map((f) => f.id),
        albumMbid,
        releaseMbid,
      )
      .then((r) => {
        setReleaseTitle(r.releaseTitle);
        setSuggestions(r.suggestions);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setSuggesting(false));
  };

  const approve = (s: TrackSuggestion) => {
    setApplyingId(s.trackFileId);
    api
      .matchTrackFile(s.trackFileId, s.recordingMbid, s.releaseMbid)
      .then(() => {
        setApprovedIds((prev) => new Set(prev).add(s.trackFileId));
        onApplied();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setApplyingId(null));
  };

  const approveAll = async () => {
    if (!suggestions) return;
    for (const s of suggestions) {
      if (approvedIds.has(s.trackFileId)) continue;
      setApplyingId(s.trackFileId);
      try {
        await api.matchTrackFile(s.trackFileId, s.recordingMbid, s.releaseMbid);
        setApprovedIds((prev) => new Set(prev).add(s.trackFileId));
      } catch (err) {
        onError(String(err instanceof Error ? err.message : err));
      }
    }
    setApplyingId(null);
    onApplied();
  };

  const pending = (suggestions ?? []).filter((s) => !approvedIds.has(s.trackFileId));
  const fileById = new Map(files.map((f) => [f.id, f]));

  // Keep the parent's "hide these from the plain file list" set in sync —
  // and clear it on close/unmount so files reappear below if the panel is
  // dismissed before every suggestion is approved.
  useEffect(() => {
    onPendingChange(new Set(pending.map((s) => s.trackFileId)));
    return () => onPendingChange(new Set());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [suggestions, approvedIds]);

  return (
    <div className="add-panel">
      <form className="search-form" onSubmit={(e) => { e.preventDefault(); suggest(); }}>
        <select
          value={artistId}
          onChange={(e) => pickArtist(e.target.value === "" ? "" : Number(e.target.value))}
        >
          <option value="">Artist…</option>
          {artists.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
        <select value={albumMbid} onChange={(e) => pickAlbum(e.target.value)} disabled={!albums}>
          <option value="">{albums ? "Album…" : "Pick an artist first"}</option>
          {albums?.map((al) => (
            <option key={al.mbid} value={al.mbid}>
              {al.title} ({al.sub})
            </option>
          ))}
        </select>
        <select
          value={releaseMbid}
          onChange={(e) => setReleaseMbid(e.target.value)}
          disabled={!versions || versions.length === 0}
        >
          <option value="">
            {!albumMbid ? "Pick an album first" : versions ? (versions.length ? "Version…" : "No cached versions") : "Loading…"}
          </option>
          {versions?.map((v) => (
            <option key={v.releaseMbid} value={v.releaseMbid}>
              {v.title}
              {v.disambiguation ? ` (${v.disambiguation})` : ""}
              {v.mediaSummary ? ` — ${v.mediaSummary}` : ""}
              {v.trackCount ? ` · ${v.trackCount} tracks` : ""}
              {v.country ? ` · ${v.country}` : ""}
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={autoMatching}
          title="Fill in the artist/album/version dropdowns above automatically, when confident enough — always yours to review and change before matching"
          onClick={autoMatch}
        >
          {autoMatching ? "Auto-matching…" : "Auto-match"}
        </button>
        <button type="submit" disabled={!albumMbid || suggesting}>
          {suggesting ? "Matching…" : "Suggest matches"}
        </button>
      </form>
      {albums && albums.length === 0 && (
        <p className="muted">This artist has no wanted or missing albums to match against.</p>
      )}

      {suggestions && (
        <>
          <div className="row">
            <span className="muted">
              {releaseTitle && (
                <>
                  Matched against <strong>{releaseTitle}</strong> —{" "}
                </>
              )}
              {suggestions.length} of {files.length} file(s) confidently slotted.
              {suggestions.length === 0 && " Try a different album, or match these by hand below."}
            </span>
            {pending.length > 1 && (
              <button className="toggle" disabled={applyingId !== null} onClick={approveAll}>
                Approve all ({pending.length})
              </button>
            )}
          </div>
          {pending.length > 0 && (
            <ul className="rows nested">
              {pending.map((s) => {
                const file = fileById.get(s.trackFileId);
                return (
                  <li key={s.trackFileId}>
                    <div className="row">
                      <span>
                        {file ? splitPath(file.path).name : `file ${s.trackFileId}`}
                        <span className="muted">
                          {" "}
                          → {s.discNumber > 1 ? `${s.discNumber}.` : ""}
                          {String(s.trackNumber).padStart(2, "0")} — {s.trackTitle}
                        </span>
                      </span>
                      <span className="row-actions">
                        <button disabled={applyingId !== null} onClick={() => approve(s)}>
                          {applyingId === s.trackFileId ? "Approving…" : "Approve"}
                        </button>
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          {pending.length === 0 && suggestions.length > 0 && (
            <p className="notice ok">✓ All suggested matches approved.</p>
          )}
        </>
      )}
    </div>
  );
}

function UnmatchedFileRow({
  file,
  busy,
  onBusy,
  onDone,
  onRemove,
  onError,
}: {
  file: MusicTrackFile;
  busy: boolean;
  onBusy: () => void;
  onDone: () => void;
  onRemove: () => void;
  onError: (message: string) => void;
}) {
  const tags = parseTags(file.tagsJson);
  const { name } = splitPath(file.path);
  const tagArtist = tags.Artist || tags.AlbumArtist || "";
  const label =
    tagArtist || tags.Album || tags.Title
      ? [tagArtist, tags.Album, tags.Title].filter(Boolean).join(" – ")
      : name;

  const [open, setOpen] = useState(false);
  const [artist, setArtist] = useState(tagArtist);
  const [album, setAlbum] = useState(tags.Album ?? "");
  const [title, setTitle] = useState(tags.Title ?? "");
  const [results, setResults] = useState<MusicBrainzRecordingResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [searched, setSearched] = useState(false);

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    setSearching(true);
    api
      .searchMusicBrainzRecordings(artist, album, title)
      .then((r) => {
        setResults(r);
        setSearched(true);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setSearching(false));
  };

  const match = (recordingMbid: string) => {
    onBusy();
    api
      .matchTrackFile(file.id, recordingMbid)
      .then(() => {
        setOpen(false);
        onDone();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  return (
    <>
      <div className="row">
        <button className="link" onClick={() => setOpen(!open)} title={file.path}>
          {open ? "▾" : "▸"} {label}
        </button>
        <span className="row-actions">
          <span className="muted">
            {file.format} · {formatBytes(file.sizeBytes)}
          </span>
          <button className="danger" disabled={busy} onClick={onRemove}>
            delete
          </button>
        </span>
      </div>
      {open && (
        <div className="missing-detail">
          {label !== name && <p className="file-path muted">{file.path}</p>}
          <form onSubmit={search} className="search-form">
            <input placeholder="Artist" value={artist} onChange={(e) => setArtist(e.target.value)} autoFocus />
            <input placeholder="Album" value={album} onChange={(e) => setAlbum(e.target.value)} />
            <input placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} />
            <button type="submit" disabled={searching}>
              {searching ? "Searching…" : "Search MusicBrainz"}
            </button>
          </form>
          {searched && results.length === 0 && !searching && (
            <p className="muted">No matches — try adjusting the search terms above.</p>
          )}
          {results.length > 0 && (
            <ul className="rows nested">
              {results.map((r) => (
                <li key={r.id}>
                  <div className="row">
                    <span>
                      {r.title}
                      {r.length > 0 && <span className="muted"> · {formatDuration(r.length)}</span>}
                    </span>
                    <span className="row-actions">
                      <button disabled={busy} onClick={() => match(r.id)}>
                        Match
                      </button>
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </>
  );
}

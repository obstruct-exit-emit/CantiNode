import { useCallback, useEffect, useState } from "react";
import {
  api,
  type MusicArtist,
  type MusicBrainzRecordingResult,
  type MusicReleaseGroup,
  type MusicTrackFile,
  type TrackSuggestion,
  type WantedAlbum,
} from "../api";
import { RowsSkeleton } from "../components/Skeleton";
import { formatBytes, formatDuration } from "../format";

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
  const [files, setFiles] = useState<MusicTrackFile[] | null>(null);
  const [artists, setArtists] = useState<MusicArtist[]>([]);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [filter, setFilter] = useState("");
  const [autoMatchDir, setAutoMatchDir] = useState<string | null>(null);

  const reload = useCallback(() => {
    api
      .listUnmatchedTrackFiles()
      .then(setFiles)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);
  useEffect(() => {
    api.listMusicArtists().then(setArtists).catch(() => {}); // the auto-match dropdown just starts empty on failure
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

  const groups = new Map<string, MusicTrackFile[]>();
  for (const f of filtered) {
    const { dir } = splitPath(f.path);
    const list = groups.get(dir);
    if (list) list.push(f);
    else groups.set(dir, [f]);
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
                    onApplied={reload}
                    onError={onError}
                  />
                )}
                <ul className="rows">
                  {groupFiles.map((f) => (
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

// AutoMatchPanel proposes track slots for a whole batch of unmatched files
// (normally one folder) against a release the user picks from their own
// artist's wanted/missing albums — cascading dropdowns (album only
// enabled once an artist is chosen) rather than a fresh MusicBrainz
// search, since the point is reusing what's already in the library. Never
// applies anything on its own: "Suggest matches" only previews a
// proposal, and each suggestion still needs its own "Approve" click (or
// "Approve all", once the proposal itself has been reviewed).
function AutoMatchPanel({
  files,
  artists,
  onApplied,
  onError,
}: {
  files: MusicTrackFile[];
  artists: MusicArtist[];
  onApplied: () => void;
  onError: (message: string) => void;
}) {
  const [artistId, setArtistId] = useState<number | "">("");
  const [albums, setAlbums] = useState<{ mbid: string; title: string; sub: string }[] | null>(null);
  const [albumMbid, setAlbumMbid] = useState("");
  const [releaseTitle, setReleaseTitle] = useState("");
  const [suggestions, setSuggestions] = useState<TrackSuggestion[] | null>(null);
  const [suggesting, setSuggesting] = useState(false);
  const [applyingId, setApplyingId] = useState<number | null>(null);
  const [approvedIds, setApprovedIds] = useState<Set<number>>(new Set());

  const pickArtist = (id: number | "") => {
    setArtistId(id);
    setAlbums(null);
    setAlbumMbid("");
    setSuggestions(null);
    if (id === "") return;
    Promise.all([api.listWantedMusicAlbums(id), api.listMissingMusicReleaseGroups(id)])
      .then(([wanted, missing]) => {
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
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
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
        <select
          value={albumMbid}
          onChange={(e) => setAlbumMbid(e.target.value)}
          disabled={!albums}
        >
          <option value="">{albums ? "Album…" : "Pick an artist first"}</option>
          {albums?.map((al) => (
            <option key={al.mbid} value={al.mbid}>
              {al.title} ({al.sub})
            </option>
          ))}
        </select>
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

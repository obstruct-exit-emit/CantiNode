import { useCallback, useEffect, useState } from "react";
import { api, type MusicBrainzRecordingResult, type MusicTrackFile } from "../api";
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
// batch beats one long undifferentiated list) and each row's search form
// starts pre-filled from the file's own tags instead of empty.
export default function UnmatchedFilesView({ onError }: { onError: (message: string) => void }) {
  const [files, setFiles] = useState<MusicTrackFile[] | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [filter, setFilter] = useState("");

  const reload = useCallback(() => {
    api
      .listUnmatchedTrackFiles()
      .then(setFiles)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

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
        Scanned but not confidently matched to a MusicBrainz recording —
        search for the right one below (pre-filled from the file's own
        tags where it has any), or delete it.
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
          {sortedDirs.map((dir) => (
            <div key={dir}>
              {sortedDirs.length > 1 && (
                <h3 className="group-heading">
                  {dir || "(root)"} ({groups.get(dir)!.length})
                </h3>
              )}
              <ul className="rows">
                {groups.get(dir)!.map((f) => (
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
          ))}
        </>
      )}
    </section>
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

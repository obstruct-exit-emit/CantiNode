import { useCallback, useEffect, useState } from "react";
import {
  api,
  proxiedImage,
  type MusicArtist,
  type MusicBrainzArtistResult,
  type MusicTrackFile,
} from "../api";
import { PosterGridSkeleton } from "../components/Skeleton";

// The Music library — a *arr-style poster grid of artists; clicking one
// opens their full detail page (albums, tracks, missing releases). Mirrors
// BooksLibraryView's shape, adapted to musiclibrary's Artist/Album/Track
// domain instead of Author/Book.
export default function MusicLibraryView({
  onError,
  onOpenArtist,
}: {
  onError: (message: string) => void;
  onOpenArtist: (id: number) => void;
}) {
  const [artists, setArtists] = useState<MusicArtist[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyHeader, setBusyHeader] = useState(false);
  const [notice, setNotice] = useState("");
  const [showAdd, setShowAdd] = useState(false);
  const [filter, setFilter] = useState("");
  const [visible, setVisible] = useState(60);

  const reload = useCallback(() => {
    api
      .listMusicArtists()
      .then(setArtists)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError]);

  useEffect(reload, [reload]);

  const headerAction = (action: () => Promise<string>) => {
    setBusyHeader(true);
    setNotice("");
    action()
      .then((msg) => {
        setNotice(msg);
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyHeader(false));
  };

  const scan = () =>
    headerAction(async () => {
      await api.triggerMusicScan();
      // The scan runs in the background (MusicBrainz is rate-limited to
      // about 1 req/sec) — poll status once, immediately, just to report
      // that it started; the unmatched card below refreshes on its own.
      return "Scan started — this can take a while for a large library.";
    });

  if (loading) return <PosterGridSkeleton />;

  return (
    <>
      <section className="card">
        <div className="card-head">
          <h2>Music — Artists ({artists.length})</h2>
          <span className="row-actions">
            <button onClick={() => setShowAdd(!showAdd)}>{showAdd ? "Close" : "+ Add"}</button>
            <button disabled={busyHeader} onClick={scan} title="Scan root folders for new files">
              Scan files
            </button>
          </span>
        </div>
        {notice && <p className="muted">{notice}</p>}

        {showAdd && <AddArtistPanel onAdded={() => { setShowAdd(false); reload(); }} onError={onError} />}

        {artists.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon" aria-hidden="true">
              🎵
            </span>
            <h3>Your music library is empty</h3>
            <p className="muted">
              Two ways to fill it: search for an artist and monitor them, or
              point a root folder at files you already own and scan.
            </p>
            <div className="settings-actions">
              <button onClick={() => setShowAdd(true)}>+ Add an artist</button>
              <button className="toggle" disabled={busyHeader} onClick={scan}>
                Scan files
              </button>
            </div>
          </div>
        ) : (
          (() => {
            const filtered = artists.filter((a) =>
              a.name.toLowerCase().includes(filter.toLowerCase()),
            );
            return (
              <>
                {artists.length > 10 && (
                  <input
                    className="grid-filter"
                    placeholder="Filter artists…"
                    value={filter}
                    onChange={(e) => {
                      setFilter(e.target.value);
                      setVisible(60);
                    }}
                  />
                )}
                <div className="poster-grid">
                  {filtered.slice(0, visible).map((a) => (
                    <button key={a.id} className="poster-card" onClick={() => onOpenArtist(a.id)}>
                      {a.imageUrl ? (
                        <img className="poster" src={proxiedImage(a.imageUrl)} alt="" loading="lazy" />
                      ) : (
                        <div className="poster fallback">{a.name.charAt(0)}</div>
                      )}
                      <span className="poster-title">{a.name}</span>
                      <span className="poster-sub">
                        {a.ownedAlbumCount ?? 0} album{(a.ownedAlbumCount ?? 0) === 1 ? "" : "s"}
                        {!a.isMonitored && " · unmonitored"}
                      </span>
                    </button>
                  ))}
                </div>
                {filtered.length === 0 && <p className="muted">No artists match the filter.</p>}
                {filtered.length > visible && (
                  <button className="toggle show-more" onClick={() => setVisible(visible + 120)}>
                    Show more ({filtered.length - visible} more)
                  </button>
                )}
              </>
            );
          })()
        )}
      </section>

      <UnmatchedTrackFilesCard onError={onError} />
    </>
  );
}

// AddArtistPanel searches MusicBrainz by name and monitors the picked
// artist — CantiNode's own original search-to-monitor flow.
function AddArtistPanel({
  onAdded,
  onError,
}: {
  onAdded: () => void;
  onError: (message: string) => void;
}) {
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<MusicBrainzArtistResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [addingMbid, setAddingMbid] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const [searched, setSearched] = useState(false);

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    if (!term.trim()) return;
    setBusy(true);
    setNotice("");
    api
      .searchMusicArtists(term)
      .then((r) => {
        setResults(r);
        setSearched(true);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const monitor = (mbid: string) => {
    setAddingMbid(mbid);
    api
      .monitorMusicArtist(mbid)
      .then(() => {
        setNotice("✓ Artist added and monitored");
        onAdded();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setAddingMbid(null));
  };

  return (
    <div className="add-panel">
      <form onSubmit={search} className="search-form">
        <input
          placeholder="Search MusicBrainz for an artist…"
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          autoFocus
        />
        <button type="submit" disabled={busy || !term.trim()}>
          {busy ? "Searching…" : "Search"}
        </button>
      </form>
      {notice && (
        <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>
      )}
      {!busy && !searched && (
        <p className="muted">
          Search MusicBrainz for an artist — pick the right one to start
          tracking their discography.
        </p>
      )}
      {searched && results.length === 0 && !busy && (
        <p className="muted">No matches on MusicBrainz.</p>
      )}
      {results.length > 0 && (
        <ul className="rows">
          {results.map((a) => (
            <li key={a.id}>
              <div className="row">
                <span>{a.name}</span>
                <span className="row-actions">
                  <button disabled={addingMbid !== null} onClick={() => monitor(a.id)}>
                    {addingMbid === a.id ? "Adding…" : "+ Add & Monitor"}
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// UnmatchedTrackFilesCard: scanned audio files the scanner couldn't
// confidently match — the manual-review queue. Each row can search
// MusicBrainz by artist/album/title and pick the right recording.
function UnmatchedTrackFilesCard({ onError }: { onError: (message: string) => void }) {
  const [files, setFiles] = useState<MusicTrackFile[] | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);

  const reload = useCallback(() => {
    api
      .listUnmatchedTrackFiles()
      .then(setFiles)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  if (!files || files.length === 0) return null;

  const remove = (id: number) => {
    setBusyId(id);
    api
      .deleteTrackFile(id)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyId(null));
  };

  return (
    <section className="card">
      <h2>Unmatched files ({files.length})</h2>
      <p className="muted">
        Scanned but not confidently matched to a MusicBrainz recording.
        Search for the right one, or delete the file.
      </p>
      <ul className="rows">
        {files.map((f) => (
          <li key={f.id}>
            <UnmatchedTrackFileRow
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
    </section>
  );
}

function UnmatchedTrackFileRow({
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
  const [open, setOpen] = useState(false);
  const [artist, setArtist] = useState("");
  const [album, setAlbum] = useState("");
  const [title, setTitle] = useState("");
  const [results, setResults] = useState<
    { id: string; title: string; length: number; score: number }[]
  >([]);
  const [searching, setSearching] = useState(false);

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    setSearching(true);
    api
      .searchMusicBrainzRecordings(artist, album, title)
      .then(setResults)
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
        <button className="link" onClick={() => setOpen(!open)}>
          {open ? "▾" : "▸"} {file.path}
        </button>
        <span className="row-actions">
          <span className="muted">{file.format}</span>
          <button className="danger" disabled={busy} onClick={onRemove}>
            delete
          </button>
        </span>
      </div>
      {open && (
        <div className="missing-detail">
          <form onSubmit={search} className="search-form">
            <input placeholder="Artist" value={artist} onChange={(e) => setArtist(e.target.value)} />
            <input placeholder="Album" value={album} onChange={(e) => setAlbum(e.target.value)} />
            <input placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} />
            <button type="submit" disabled={searching}>
              {searching ? "Searching…" : "Search MusicBrainz"}
            </button>
          </form>
          {results.length > 0 && (
            <ul className="rows nested">
              {results.map((r) => (
                <li key={r.id}>
                  <div className="row">
                    <span>{r.title}</span>
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

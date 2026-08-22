import { useCallback, useEffect, useState } from "react";
import { api, proxiedImage, type MusicArtist, type MusicBrainzArtistResult } from "../api";
import { PosterGridSkeleton } from "../components/Skeleton";
import { SortSelect, DirectionButtons, sortArtists, defaultDirFor, type SortDir } from "../components/SortControl";

// Library display: "grid" (current default, large covers), "compact" (same
// grid, smaller covers), or "list" (a plain name + status row) — mirrors
// ArtistDetailView's own AlbumsView toggle, one level up (artists instead
// of albums).
type LibraryView = "grid" | "compact" | "list";

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
  const [addMode, setAddMode] = useState<"artist" | "series">("artist");
  const [filter, setFilter] = useState("");
  const [visible, setVisible] = useState(60);
  const [sort, setSort] = useState("name");
  const [sortDir, setSortDir] = useState<SortDir>(defaultDirFor("name"));
  const [view, setViewState] = useState<LibraryView>("grid");
  const changeSort = (key: string) => {
    setSort(key);
    setSortDir(defaultDirFor(key));
  };

  const reload = useCallback(() => {
    api
      .listMusicArtists()
      .then(setArtists)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError]);

  useEffect(reload, [reload]);

  // The signed-in account's own remembered library view (separate from an
  // artist page's own Albums-section view — see ArtistDetailView) — loaded
  // once on mount, best-effort (a fetch failure just leaves the "grid"
  // default in place rather than blocking the page).
  useEffect(() => {
    api
      .getViewPrefs()
      .then((p) => {
        if (p.libraryView === "grid" || p.libraryView === "compact" || p.libraryView === "list") {
          setViewState(p.libraryView);
        }
      })
      .catch(() => {});
  }, []);
  const setView = (v: LibraryView) => {
    setViewState(v);
    api.setViewPrefs({ libraryView: v }).catch(() => {});
  };

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

        {showAdd && (
          <>
            <span className="view-toggle">
              <button
                type="button"
                className={addMode === "artist" ? "toggle on" : "toggle"}
                onClick={() => setAddMode("artist")}
              >
                Search artist
              </button>
              <button
                type="button"
                className={addMode === "series" ? "toggle on" : "toggle"}
                onClick={() => setAddMode("series")}
                title="e.g. a numbered compilation series like Now That's What I Call Music!"
              >
                Paste series link
              </button>
            </span>
            {addMode === "artist" ? (
              <AddArtistPanel onAdded={() => { setShowAdd(false); reload(); }} onError={onError} />
            ) : (
              <AddSeriesPanel onAdded={() => { setShowAdd(false); reload(); }} />
            )}
          </>
        )}

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
            const filtered = sortArtists(
              artists.filter((a) => a.name.toLowerCase().includes(filter.toLowerCase())),
              sort,
              sortDir,
            );
            return (
              <>
                {artists.length > 1 && (
                  <div className="grid-controls">
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
                    <span className="view-toggle">
                      {(["grid", "compact", "list"] as const).map((v) => (
                        <button
                          key={v}
                          type="button"
                          className={view === v ? "toggle on" : "toggle"}
                          onClick={() => setView(v)}
                          title={v === "grid" ? "Covers" : v === "compact" ? "Smaller covers" : "List"}
                        >
                          {v === "grid" ? "Grid" : v === "compact" ? "Compact" : "List"}
                        </button>
                      ))}
                    </span>
                    {artists.length > 1 && (
                      <>
                        <SortSelect
                          value={sort}
                          onChange={changeSort}
                          options={[
                            ["name", "Name"],
                            ["added", "Recently added"],
                            ["albums", "Album count"],
                            ["missing", "Missing count"],
                          ]}
                        />
                        <DirectionButtons value={sortDir} onChange={setSortDir} />
                      </>
                    )}
                  </div>
                )}
                {view === "list" ? (
                  <ul className="rows">
                    {filtered.slice(0, visible).map((a) => (
                      <li key={a.id}>
                        <div className="row">
                          <button className="link" onClick={() => onOpenArtist(a.id)}>
                            {a.name}
                          </button>
                          <span className="row-actions">
                            <span className="muted">
                              {a.totalAlbumCount ? (
                                <>
                                  {a.ownedAlbumCount ?? 0}/{a.totalAlbumCount} owned
                                </>
                              ) : (
                                <>
                                  {a.ownedAlbumCount ?? 0} album{(a.ownedAlbumCount ?? 0) === 1 ? "" : "s"}
                                </>
                              )}
                            </span>
                            {!a.isMonitored && <span className="muted">unmonitored</span>}
                          </span>
                        </div>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <div className={view === "compact" ? "poster-grid compact" : "poster-grid"}>
                    {filtered.slice(0, visible).map((a) => (
                      <button key={a.id} className="poster-card" onClick={() => onOpenArtist(a.id)}>
                        {a.imageUrl ? (
                          <img className="poster" src={proxiedImage(a.imageUrl)} alt="" loading="lazy" />
                        ) : (
                          <div className="poster fallback">{a.name.charAt(0)}</div>
                        )}
                        <span className="poster-title">{a.name}</span>
                        <span className="poster-sub">
                          {a.totalAlbumCount ? (
                            <>
                              {a.ownedAlbumCount ?? 0}/{a.totalAlbumCount} owned
                            </>
                          ) : (
                            <>
                              {a.ownedAlbumCount ?? 0} album{(a.ownedAlbumCount ?? 0) === 1 ? "" : "s"}
                            </>
                          )}
                          {!a.isMonitored && " · unmonitored"}
                        </span>
                      </button>
                    ))}
                  </div>
                )}
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
          {results.map((a) => {
            // No image available at all for a not-yet-added search result
            // (MusicBrainz's artist search doesn't return one), so a list
            // row — full-width, no wasted card space on a placeholder tile —
            // shows this metadata more usefully than a poster grid did.
            const meta = [a.type, a.country, a.disambiguation].filter(Boolean).join(" · ");
            return (
              <li key={a.id}>
                <div className="row">
                  <span>
                    {a.name}
                    {meta && <span className="muted"> — {meta}</span>}
                  </span>
                  <span className="row-actions">
                    <button disabled={addingMbid !== null} onClick={() => monitor(a.id)}>
                      {addingMbid === a.id ? "Adding…" : "+ Add & Monitor"}
                    </button>
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// AddSeriesPanel adds a MusicBrainz Series (e.g. a numbered compilation
// series like "Now That's What I Call Music!") as a synthetic library
// artist, tracking its whole discography going forward — CantiNode's
// second way to add music beyond one real artist at a time. No search
// step like AddArtistPanel has: the pasted link/ID already names exactly
// what to add, so submit goes straight to the backend, which does the
// actual MusicBrainz lookup and validation (see api.addMusicSeries).
function AddSeriesPanel({ onAdded }: { onAdded: () => void }) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim()) return;
    setBusy(true);
    setNotice("");
    api
      .addMusicSeries(input)
      .then((a) => {
        setNotice(`✓ "${a.name}" added and monitored`);
        setInput("");
        onAdded();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  return (
    <div className="add-panel">
      <form onSubmit={submit} className="search-form">
        <input
          placeholder="Paste a MusicBrainz series link or ID…"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          autoFocus
        />
        <button type="submit" disabled={busy || !input.trim()}>
          {busy ? "Adding…" : "Add & Monitor"}
        </button>
      </form>
      {notice && (
        <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>
      )}
      {!busy && !notice && (
        <p className="muted">
          For a numbered compilation series (e.g. "Now That's What I Call
          Music!") — every entry is tracked as an album under one artist
          page, same as a real artist's discography.
        </p>
      )}
    </div>
  );
}


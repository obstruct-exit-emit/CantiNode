import { useCallback, useEffect, useState } from "react";
import { api, proxiedImage, type RenameMove, type Series, type SeriesResult } from "../api";
import { libraryLabels } from "../App";
import AddResultsGrid, { type AddResult } from "../components/AddResultsGrid";
import { PosterGridSkeleton } from "../components/Skeleton";
import UnmatchedCard from "../components/UnmatchedCard";
import WantedCard from "../components/WantedCard";

// A series-first library area (Manga, Comics, or Magazines) — a *arr-style
// poster grid of series; clicking one opens its full detail page. Adding is
// scoped to this library.
export default function SeriesLibraryView({
  mediaType,
  onError,
  onOpenSeries,
}: {
  mediaType: string;
  onError: (message: string) => void;
  onOpenSeries: (id: number) => void;
}) {
  const [series, setSeries] = useState<Series[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  // Large libraries: filter client-side and render the grid incrementally.
  const [filter, setFilter] = useState("");
  const [visible, setVisible] = useState(60);
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  // Library-scope organize also previews unwanted files it can delete.
  const [cleanupPlan, setCleanupPlan] = useState<{ path: string; size: number }[]>([]);
  const [deleteUnwanted, setDeleteUnwanted] = useState(true);

  const reload = useCallback(() => {
    api
      .listSeries()
      .then((all) => setSeries(all.filter((s) => s.mediaType === mediaType)))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError, mediaType]);

  useEffect(reload, [reload]);

  const searchWanted = () => {
    setBusy(true);
    setNotice("");
    api
      .searchWanted()
      .then((r) =>
        setNotice(
          r.searched === 0
            ? "Nothing to search — everything monitored is owned (or pending)."
            : `Searched ${r.searched} wanted item(s), grabbed ${r.grabbed}.`,
        ),
      )
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const scan = () => {
    setBusy(true);
    setNotice("");
    api
      .scan(mediaType)
      .then((r) => {
        setNotice(
          r.roots === 0
            ? `No ${libraryLabels[mediaType] ?? mediaType} root folders to scan — add one under Settings.`
            : `Scanned ${r.scanned} file(s): ${r.matched} matched, ${r.unmatched} unmatched.`,
        );
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const refreshAll = () => {
    setBusy(true);
    setNotice("");
    api
      .refreshLibrary(mediaType)
      .then((r) => setNotice(r.message))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const previewRenames = async () => {
    setBusy(true);
    setNotice("");
    try {
      // Scan this library first so the plan reflects what's actually on disk.
      await api.scan(mediaType);
      const r = await api.renamePreview(undefined, undefined, mediaType);
      setRenamePlan(r.moves);
      setCleanupPlan(r.cleanups ?? []);
      setDeleteUnwanted(true);
      if (r.moves.length === 0 && (r.cleanups ?? []).length === 0) {
        setNotice("All files already match the naming templates — nothing to clean up.");
      }
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusy(false);
    }
  };

  const applyRenames = () => {
    setBusy(true);
    api
      .renameApply(undefined, undefined, mediaType, undefined, deleteUnwanted && cleanupPlan.length > 0)
      .then((r) => {
        const bits = [`Moved ${r.moves.length} file(s)`];
        if (r.deleted) bits.push(`deleted ${r.deleted} unwanted`);
        if (r.prunedDirs) bits.push(`pruned ${r.prunedDirs} empty folder(s)`);
        if (r.skips.length) bits.push(`${r.skips.length} skipped`);
        setNotice(bits.join(", ") + ".");
        setRenamePlan(null);
        setCleanupPlan([]);
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  if (loading) return <PosterGridSkeleton />;

  return (
    <>
    <section className="card">
      <div className="card-head">
        <h2>
          {libraryLabels[mediaType] ?? mediaType} ({series.length})
        </h2>
        <span className="row-actions">
          <button onClick={() => setShowAdd(!showAdd)}>{showAdd ? "Close" : "+ Add"}</button>
          {mediaType !== "magazine" && (
            <button disabled={busy} onClick={searchWanted}>Search wanted</button>
          )}
          <button disabled={busy} onClick={previewRenames} title="Preview naming-template moves">
            Organize…
          </button>
          <button disabled={busy} onClick={scan}>Scan files</button>
          {mediaType !== "magazine" && (
            <button
              disabled={busy}
              onClick={refreshAll}
              title="Re-sync every series in this library from the metadata provider (runs in the background)"
            >
              Refresh metadata
            </button>
          )}
        </span>
      </div>
      {notice && <p className="muted">{notice}</p>}

      {renamePlan && (renamePlan.length > 0 || cleanupPlan.length > 0) && (
        <div className="rename-plan">
          {renamePlan.length > 0 && (
            <>
              <p>{renamePlan.length} file(s) would move to match the naming templates:</p>
              <ul className="rows">
                {renamePlan.map((m) => (
                  <li key={m.fileId}>
                    <div className="move">
                      <span className="file-path muted">{m.from}</span>
                      <span className="file-path">→ {m.to}</span>
                    </div>
                  </li>
                ))}
              </ul>
            </>
          )}
          {cleanupPlan.length > 0 && (
            <>
              <p>
                {cleanupPlan.length} file(s) don't belong in this library (junk or
                another type's media — matched files, sidecars, and artwork are kept):
              </p>
              <ul className="rows">
                {cleanupPlan.map((c) => (
                  <li key={c.path}>
                    <div className="move">
                      <span className="file-path muted">🗑 {c.path}</span>
                    </div>
                  </li>
                ))}
              </ul>
              <label className="check">
                <span>
                  <input
                    type="checkbox"
                    checked={deleteUnwanted}
                    onChange={(e) => setDeleteUnwanted(e.target.checked)}
                  />{" "}
                  Delete these {cleanupPlan.length} file(s) from disk and prune empty folders
                </span>
              </label>
            </>
          )}
          <div className="settings-actions">
            <button disabled={busy} onClick={applyRenames}>Apply</button>
            <button
              className="toggle"
              onClick={() => {
                setRenamePlan(null);
                setCleanupPlan([]);
              }}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {showAdd &&
        (mediaType === "magazine" ? (
          <AddMagazinePanel onAdded={reload} />
        ) : (
          <AddSeriesPanel mediaType={mediaType} onAdded={reload} />
        ))}

      {series.length === 0 ? (
        <div className="empty-state">
          <span className="empty-icon" aria-hidden="true">
            {mediaType === "manga" ? "🀄" : mediaType === "comic" ? "💥" : "📰"}
          </span>
          <h3>Your {(libraryLabels[mediaType] ?? mediaType).toLowerCase()} library is empty</h3>
          <p className="muted">
            {mediaType === "magazine"
              ? "Add a magazine by name — issues are recognized from file names when you scan, and organized into clean folders."
              : `Search for a ${mediaType} series and monitor the volumes you want, or scan a root folder with files you already own.`}
          </p>
          <div className="settings-actions">
            <button onClick={() => setShowAdd(true)}>
              {mediaType === "magazine" ? "+ Add a magazine" : "+ Add a series"}
            </button>
            <button className="toggle" disabled={busy} onClick={scan}>
              Scan files
            </button>
          </div>
        </div>
      ) : (
        (() => {
          const filtered = series.filter((s) =>
            s.title.toLowerCase().includes(filter.toLowerCase()),
          );
          return (
            <>
              {series.length > 10 && (
                <input
                  className="grid-filter"
                  placeholder="Filter series…"
                  value={filter}
                  onChange={(e) => {
                    setFilter(e.target.value);
                    setVisible(60);
                  }}
                />
              )}
              <div className="poster-grid">
                {filtered.slice(0, visible).map((s) => (
                  <button key={s.id} className="poster-card" onClick={() => onOpenSeries(s.id)}>
                    {s.coverUrl ? (
                      <img className="poster" src={proxiedImage(s.coverUrl)} alt="" loading="lazy" />
                    ) : (
                      <div className="poster fallback">{s.title.charAt(0)}</div>
                    )}
                    <span className="poster-title">{s.title}</span>
                    <span className="poster-sub">
                      {s.ownedCount}/{s.itemCount} owned
                      {!s.monitored && " · unmonitored"}
                    </span>
                  </button>
                ))}
              </div>
              {filtered.length === 0 && <p className="muted">No series match the filter.</p>}
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

    {mediaType !== "magazine" && (
      <WantedCard key={`wanted-${mediaType}`} library={mediaType} onError={onError} />
    )}

    <UnmatchedCard
      key={`unmatched-${mediaType}`}
      mediaType={mediaType}
      seriesList={series}
      onChanged={reload}
      onError={onError}
    />
    </>
  );
}

function AddSeriesPanel({
  mediaType,
  onAdded,
}: {
  mediaType: string;
  onAdded: () => void;
}) {
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SeriesResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    if (!term.trim()) return;
    setBusy(true);
    setNotice("");
    api
      .searchSeries(term, mediaType)
      .then(setResults)
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  const gridResults: AddResult[] = results.map((r) => ({
    key: r.foreignSeriesId,
    title: r.title,
    subtitle: [
      r.year ? String(r.year) : "",
      r.authorName ?? "",
      r.issueCount > 0 ? `${r.issueCount} volumes` : "volume count TBD",
    ]
      .filter(Boolean)
      .join(" · "),
    blurb: r.description || undefined,
    imageUrl: r.coverUrl || undefined,
    addLabel: "Add series",
    add: () => api.addSeries(mediaType, r.foreignSeriesId),
  }));

  return (
    <div className="add-panel">
      <form onSubmit={search} className="search-form">
        <input
          placeholder={`Search ${mediaType} series…`}
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          autoFocus
        />
        <button type="submit" disabled={busy || !term.trim()}>
          {busy ? "Searching…" : "Search"}
        </button>
      </form>
      {notice && <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>}
      {!busy && results.length === 0 && notice === "" && (
        <p className="muted">
          Search {mediaType} series on the metadata provider — results appear
          here with cover art.
        </p>
      )}
      <AddResultsGrid results={gridResults} onAdded={onAdded} />
    </div>
  );
}

function AddMagazinePanel({ onAdded }: { onAdded: () => void }) {
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const add = (e: React.FormEvent) => {
    e.preventDefault();
    const t = title.trim();
    if (!t) return;
    setBusy(true);
    setNotice("");
    api
      .addMagazine(t)
      .then(() => {
        setTitle("");
        setNotice(`✓ Added "${t}" — scan a magazine root and its issues match by date or number`);
        onAdded();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  return (
    <div className="add-panel">
      <form onSubmit={add} className="search-form">
        <input
          placeholder="Magazine name (e.g. The Economist) — issues match by date"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          autoFocus
        />
        <button type="submit" disabled={busy || !title.trim()}>Add magazine</button>
      </form>
      {notice && <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>}
    </div>
  );
}

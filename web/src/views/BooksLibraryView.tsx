import { useCallback, useEffect, useState } from "react";
import {
  api,
  proxiedImage,
  type Author,
  type Book,
  type RenameMove,
  type SearchAuthor,
  type SearchBook,
} from "../api";
import AddResultsGrid, { type AddResult } from "../components/AddResultsGrid";
import { PosterGridSkeleton } from "../components/Skeleton";
import UnmatchedCard from "../components/UnmatchedCard";
import WantedCard from "../components/WantedCard";

// One format library's area (Ebooks or Audiobooks) — a *arr-style poster
// grid of authors; clicking one opens their full detail page. Only books
// that are members of THIS library count here.
export default function BooksLibraryView({
  library,
  onError,
  onOpenAuthor,
}: {
  library: "ebook" | "audiobook";
  onError: (message: string) => void;
  onOpenAuthor: (id: number) => void;
}) {
  const label = library === "ebook" ? "Ebooks" : "Audiobooks";
  const [authors, setAuthors] = useState<Author[]>([]);
  const [libraryBooks, setLibraryBooks] = useState<Book[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyHeader, setBusyHeader] = useState(false);
  const [notice, setNotice] = useState("");
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  // Library-scope organize also previews unwanted files (junk, wrong-type
  // media) it can delete, plus empty-folder pruning on apply.
  const [cleanupPlan, setCleanupPlan] = useState<{ path: string; size: number }[]>([]);
  const [deleteUnwanted, setDeleteUnwanted] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  // Large libraries: filter client-side and render the grid incrementally.
  const [filter, setFilter] = useState("");
  const [visible, setVisible] = useState(60);

  const reload = useCallback(() => {
    // listBooks scoped to this format library — filtered server-side, not
    // the whole database's books shipped to the browser just to populate
    // the manual-match fallback list below.
    Promise.all([api.listAuthors(library), api.listBooks(undefined, library)])
      .then(([au, bk]) => {
        setAuthors(au);
        setLibraryBooks(bk);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError, library]);

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
      const r = await api.scan(library);
      const errors = r.errors?.length ? `, ${r.errors.length} root(s) failed` : "";
      return r.roots === 0
        ? `No ${label} root folders to scan — add one under Settings.`
        : `Scanned ${r.scanned} file(s): ${r.matched} matched, ${r.unmatched} unmatched, ${r.removed} removed${errors}`;
    });

  const searchWanted = () =>
    headerAction(async () => {
      const r = await api.searchWanted();
      const details = r.outcomes
        .filter((o) => !o.grabbed && o.message)
        .slice(0, 3)
        .map((o) => `${o.bookTitle}: ${o.message}`)
        .join("; ");
      return r.searched === 0
        ? "Nothing to search — every monitored item has its file (or a pending grab)."
        : `Searched ${r.searched} wanted item(s), grabbed ${r.grabbed}.${details ? " " + details : ""}`;
    });

  const previewRenames = async () => {
    setBusyHeader(true);
    setNotice("");
    try {
      // Scan this library first so the plan reflects what's actually on disk
      // — organizing from stale records misses new and moved files.
      await api.scan(library);
      const r = await api.renamePreview(undefined, undefined, library);
      setRenamePlan(r.moves);
      setCleanupPlan(r.cleanups ?? []);
      setDeleteUnwanted(true);
      if (r.moves.length === 0 && (r.cleanups ?? []).length === 0) {
        setNotice("All files already match the naming templates — nothing to clean up.");
      }
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusyHeader(false);
    }
  };

  const applyRenames = () => {
    setBusyHeader(true);
    api
      .renameApply(undefined, undefined, library, undefined, deleteUnwanted && cleanupPlan.length > 0)
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
      .finally(() => setBusyHeader(false));
  };

  if (loading) return <PosterGridSkeleton />;

  return (
    <>
      <section className="card">
        <div className="card-head">
          <h2>{label} — Authors ({authors.length})</h2>
          <span className="row-actions">
            <button onClick={() => setShowAdd(!showAdd)}>{showAdd ? "Close" : "+ Add"}</button>
            <button disabled={busyHeader} onClick={searchWanted} title="Search indexers for everything wanted">
              Search wanted
            </button>
            <button disabled={busyHeader} onClick={previewRenames} title="Preview naming-template moves">
              Organize…
            </button>
            <button disabled={busyHeader} onClick={scan} title="Scan root folders">
              Scan files
            </button>
            <button
              disabled={busyHeader}
              onClick={() => headerAction(async () => (await api.refreshLibrary(library)).message)}
              title="Re-sync every author in this library from the metadata provider (runs in the background)"
            >
              Refresh metadata
            </button>
          </span>
        </div>
        {notice && <p className="muted">{notice}</p>}

        {showAdd && (
          <AddPanel library={library} onAdded={() => { reload(); }} onError={onError} />
        )}

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
              <button disabled={busyHeader} onClick={applyRenames}>Apply</button>
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

        {authors.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon" aria-hidden="true">
              {library === "ebook" ? "📖" : "🎧"}
            </span>
            <h3>Your {label.toLowerCase()} library is empty</h3>
            <p className="muted">
              Two ways to fill it: search for an author or book and monitor
              what you want, or point a root folder at files you already own
              and scan.
            </p>
            <div className="settings-actions">
              <button onClick={() => setShowAdd(true)}>+ Add an author or book</button>
              <button className="toggle" disabled={busyHeader} onClick={scan}>
                Scan files
              </button>
            </div>
          </div>
        ) : (
          (() => {
            const filtered = authors.filter((a) =>
              a.name.toLowerCase().includes(filter.toLowerCase()),
            );
            return (
              <>
                {authors.length > 10 && (
                  <input
                    className="grid-filter"
                    placeholder="Filter authors…"
                    value={filter}
                    onChange={(e) => {
                      setFilter(e.target.value);
                      setVisible(60);
                    }}
                  />
                )}
                <div className="poster-grid">
                  {filtered.slice(0, visible).map((a) => (
                    <button key={a.id} className="poster-card" onClick={() => onOpenAuthor(a.id)}>
                      {a.imageUrl ? (
                        <img className="poster" src={proxiedImage(a.imageUrl)} alt="" loading="lazy" />
                      ) : (
                        <div className="poster fallback">{a.name.charAt(0)}</div>
                      )}
                      <span className="poster-title">{a.name}</span>
                      <span className="poster-sub">
                        {a.ownedCount}/{a.bookCount ?? 0} owned
                      </span>
                    </button>
                  ))}
                </div>
                {filtered.length === 0 && <p className="muted">No authors match the filter.</p>}
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

      <WantedCard key={`wanted-${library}`} library={library} onError={onError} />

      <UnmatchedCard
        key={`unmatched-${library}`}
        mediaType={library}
        books={libraryBooks}
        onChanged={reload}
        onError={onError}
      />
    </>
  );
}

// AddPanel searches the metadata provider and adds into THIS library.
function AddPanel({
  library,
  onAdded,
  onError,
}: {
  library: "ebook" | "audiobook";
  onAdded: () => void;
  onError: (message: string) => void;
}) {
  const [term, setTerm] = useState("");
  const [kind, setKind] = useState<"author" | "book">("author");
  const [authors, setAuthors] = useState<SearchAuthor[]>([]);
  const [books, setBooks] = useState<SearchBook[]>([]);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const search = (e: React.FormEvent) => {
    e.preventDefault();
    if (!term.trim()) return;
    setBusy(true);
    setNotice("");
    const done = () => setBusy(false);
    if (kind === "author") {
      api.searchAuthors(term).then((r) => { setAuthors(r); setBooks([]); }, (err: unknown) =>
        onError(String(err instanceof Error ? err.message : err))).finally(done);
    } else {
      api.searchBooks(term).then((r) => { setBooks(r); setAuthors([]); }, (err: unknown) =>
        onError(String(err instanceof Error ? err.message : err))).finally(done);
    }
  };

  const searched = authors.length > 0 || books.length > 0;
  const results: AddResult[] = [
    ...authors.map((a) => ({
      key: a.foreignAuthorId,
      title: a.name,
      subtitle: a.bookCount ? `${a.bookCount} books` : undefined,
      imageUrl: a.imageUrl || undefined,
      addLabel: "Add author",
      add: () => api.addAuthor(a.foreignAuthorId, library),
    })),
    ...books.map((b) => ({
      key: b.foreignBookId,
      title: b.title,
      subtitle:
        b.authorName + (b.releaseDate ? ` · ${b.releaseDate.slice(0, 4)}` : ""),
      imageUrl: b.coverUrl || undefined,
      addLabel: "Add book",
      add: () => api.addBook(b.foreignBookId, library),
    })),
  ];

  return (
    <div className="add-panel">
      <form onSubmit={search} className="search-form">
        <select value={kind} onChange={(e) => setKind(e.target.value as "author" | "book")}>
          <option value="author">Author</option>
          <option value="book">Book</option>
        </select>
        <input
          placeholder="Search the metadata provider…"
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
      {!busy && !searched && notice === "" && (
        <p className="muted">
          Search {kind === "author" ? "authors" : "books"} on the metadata
          provider — results appear here with cover art.
        </p>
      )}
      <AddResultsGrid results={results} onAdded={onAdded} />
    </div>
  );
}

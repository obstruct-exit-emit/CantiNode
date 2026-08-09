import { useCallback, useEffect, useRef, useState } from "react";
import { api, proxiedImage, type Author, type Book } from "../api";
import RemovePanel from "../components/RemovePanel";
import ReleaseBrowser from "../components/ReleaseBrowser";
import { DetailSkeleton } from "../components/Skeleton";
import { downloadPct, useQueue } from "../useQueue";
import { formatBytes } from "../format";
import { useUi } from "../ui";

// Full-page book detail, mirroring the author page: header with cover art,
// about text, and status/actions, then releases, files, and edition info as
// their own cards. The back button returns to the author.
export default function BookDetailView({
  id,
  library,
  onError,
  onBack,
}: {
  id: number;
  library: "ebook";
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const { confirmDlg } = useUi();
  const [book, setBook] = useState<Book | null>(null);
  const [author, setAuthor] = useState<Author | null>(null);
  const [showReleases, setShowReleases] = useState(false);
  const [searching, setSearching] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [grabNotice, setGrabNotice] = useState("");
  const [fileBusy, setFileBusy] = useState(false);
  const [fileNotice, setFileNotice] = useState("");

  const reload = useCallback(() => {
    api
      .getBook(id)
      .then((b) => {
        setBook(b);
        return api.getAuthor(b.authorId).then(setAuthor);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [id, onError]);

  useEffect(reload, [reload]);

  // Live download status for this book (shared, server-cached queue poll).
  // When an active download disappears — imported, failed, removed —
  // refresh the book so the badge flips to owned or back to wanted.
  const { refresh, activeFor } = useQueue();
  const dl = activeFor(id, library);
  const hadDl = useRef(false);
  useEffect(() => {
    if (hadDl.current && !dl) reload();
    hadDl.current = dl !== null;
  }, [dl, reload]);

  if (!book) return <DetailSkeleton />;

  const owned = book.hasEbookFile;
  const monitored = book.ebookMonitored;
  const files = (book.files ?? []).filter((f) => f.mediaType === library);

  const setMembership = (member: boolean, mon: boolean, deleteFiles = false) => {
    api
      .setBookLibrary(book.id, library, member, mon, deleteFiles)
      .then(() => {
        // Leaving the library means the book no longer belongs on this
        // page — return to the author.
        if (!member) {
          onBack();
        } else {
          reload();
        }
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  const autoGrab = () => {
    setSearching(true);
    setGrabNotice("");
    api
      .autoSearchBook(book.id, library)
      .then((o) => {
        if (o.grabbed) {
          setGrabNotice(`✓ Grabbed "${o.release}" → ${o.client}`);
          refresh(); // show the downloading badge right away
        } else {
          setGrabNotice(`✗ ${o.message ?? "nothing grabbed"} — Search releases shows why`);
        }
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setSearching(false));
  };

  const year = book.releaseDate ? ` (${book.releaseDate.slice(0, 4)})` : "";
  const subtitle = [
    author?.name,
    book.series && book.series.length > 0
      ? book.series.map((s) => `${s.title} #${s.position}`).join(", ")
      : "",
    book.rating > 0 ? `★ ${book.rating.toFixed(1)}` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← {author?.name ?? "Author"}
      </button>

      <section className="card detail-head">
        {book.coverUrl ? (
          <img className="detail-art" src={proxiedImage(book.coverUrl)} alt={`Cover of ${book.title}`} />
        ) : (
          <div className="detail-art fallback">{book.title.charAt(0)}</div>
        )}
        <div className="detail-info">
          <h2>
            {book.title}
            <span className="muted">{year}</span>
          </h2>
          <p className="muted">
            {subtitle}
            {subtitle && " · "}
            {!owned && dl ? (
              <span className="owned dl" title={`${dl.status} on ${dl.client}`}>
                ⬇ downloading {downloadPct(dl)}
              </span>
            ) : (
              <span className={owned ? "owned yes" : "owned no"}>
                {owned ? "owned" : "wanted"}
              </span>
            )}
          </p>
          {book.description && <p className="detail-desc">{book.description}</p>}
          <div className="settings-actions">
            <button
              className={monitored ? "toggle on" : "toggle"}
              title="Whether this book is searched for automatically"
              onClick={() => setMembership(true, !monitored)}
            >
              {monitored ? "monitored" : "unmonitored"}
            </button>
            <button disabled={searching} onClick={autoGrab} title="Search indexers and grab the best release">
              {searching ? "Searching…" : "Auto grab"}
            </button>
            <button
              className={showReleases ? "toggle on" : ""}
              onClick={() => setShowReleases(!showReleases)}
              title="Browse every release candidate — sort, filter, pick one yourself"
            >
              {showReleases ? "Hide releases" : "Search releases"}
            </button>
            <button
              className="danger"
              title="Remove from the Ebooks library"
              onClick={() => setConfirmRemove(!confirmRemove)}
            >
              Remove from library
            </button>
            {grabNotice && (
              <span className={grabNotice.startsWith("✗") ? "notice bad" : "notice ok"}>{grabNotice}</span>
            )}
          </div>
          {confirmRemove && (
            <RemovePanel
              message={`Remove "${book.title}" from the Ebooks library?`}
              checkboxLabel="Also delete its file(s) from disk"
              busy={searching}
              onConfirm={(deleteFiles) => setMembership(false, false, deleteFiles)}
              onCancel={() => setConfirmRemove(false)}
            />
          )}
        </div>
      </section>

      {showReleases && (
        <section className="card">
          <h2>Releases</h2>
          <ReleaseBrowser
            bookId={book.id}
            mediaType={library}
            onGrabbed={refresh}
            onClose={() => setShowReleases(false)}
          />
        </section>
      )}

      {files.length > 0 && (
        <section className="card">
          <h2>Files ({files.length})</h2>
          {fileNotice && <p className="notice ok">{fileNotice}</p>}
          <ul className="rows">
            {files.map((f) => (
              <li key={f.id}>
                <div className="row">
                  <span className="file-path">📄 {f.path}</span>
                  <span className="row-actions">
                    <span className="muted">
                      {f.format} · {formatBytes(f.size)}
                    </span>
                    <button
                      className="toggle"
                      disabled={fileBusy}
                      title="Move this book's files to match the naming templates"
                      onClick={async () => {
                        setFileBusy(true);
                        setFileNotice("");
                        try {
                          const preview = await api.renamePreview(undefined, undefined, undefined, book.id);
                          if (preview.moves.length === 0) {
                            setFileNotice("✓ Already organized — files match the naming templates.");
                            return;
                          }
                          const ok = await confirmDlg({
                            title: "Organize files",
                            message:
                              `Move ${preview.moves.length} file(s) to match the naming templates?\n\n` +
                              preview.moves.map((m) => `${m.from}\n  → ${m.to}`).join("\n"),
                            confirmLabel: "Organize",
                          });
                          if (!ok) return;
                          const applied = await api.renameApply(undefined, undefined, undefined, book.id);
                          setFileNotice(`✓ Moved ${applied.moves.length} file(s).`);
                          reload();
                        } catch (err) {
                          onError(String(err instanceof Error ? err.message : err));
                        } finally {
                          setFileBusy(false);
                        }
                      }}
                    >
                      organize
                    </button>
                    <button
                      className="danger"
                      disabled={fileBusy}
                      title="Delete this file from disk and forget it"
                      onClick={async () => {
                        const ok = await confirmDlg({
                          title: "Delete file",
                          message: `Delete this file from disk?\n\n${f.path}\n\nThe book loses this copy; without it the book counts as wanted again.`,
                          confirmLabel: "Delete file",
                          danger: true,
                        });
                        if (!ok) return;
                        setFileBusy(true);
                        setFileNotice("");
                        try {
                          await api.dismissFile(f.id, true);
                          setFileNotice("✓ File deleted.");
                          reload();
                        } catch (err) {
                          onError(String(err instanceof Error ? err.message : err));
                        } finally {
                          setFileBusy(false);
                        }
                      }}
                    >
                      delete
                    </button>
                  </span>
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

import { useEffect, useMemo, useState } from "react";
import { api, proxiedImage, type Author, type Book, type Series } from "../api";
import { libraryLabels } from "../App";
import { RowsSkeleton } from "../components/Skeleton";

// Global search: one query across every library — authors, prose books, and
// series/magazines — matched client-side against the library lists. This
// finds what you HAVE (and track); adding new content stays per-library
// where the right metadata provider is known.
export default function SearchView({
  query,
  onError,
  onOpenAuthor,
  onOpenBook,
  onOpenSeries,
}: {
  query: string;
  onError: (message: string) => void;
  onOpenAuthor: (id: number, library: "ebook" | "audiobook") => void;
  onOpenBook: (book: Book, library: "ebook" | "audiobook") => void;
  onOpenSeries: (id: number, mediaType: string) => void;
}) {
  const [authors, setAuthors] = useState<Author[] | null>(null);
  const [books, setBooks] = useState<Book[]>([]);
  const [series, setSeries] = useState<Series[]>([]);

  useEffect(() => {
    Promise.all([api.listAuthors(), api.listBooks(), api.listSeries()])
      .then(([a, b, s]) => {
        setAuthors(a);
        setBooks(b);
        setSeries(s);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  const q = query.trim().toLowerCase();
  const hits = useMemo(() => {
    if (!authors || q === "") return null;
    const byId = new Map(authors.map((a) => [a.id, a.name]));
    return {
      authors: authors.filter((a) => a.name.toLowerCase().includes(q)).slice(0, 24),
      books: books
        .filter((b) => b.mediaType === "book" && b.title.toLowerCase().includes(q))
        .slice(0, 24)
        .map((b) => ({ book: b, authorName: byId.get(b.authorId) ?? "" })),
      series: series.filter((s) => s.title.toLowerCase().includes(q)).slice(0, 24),
    };
  }, [authors, books, series, q]);

  if (!authors) return <RowsSkeleton rows={5} />;
  if (!hits) {
    return (
      <section className="card">
        <h2>Search</h2>
        <p className="muted">Type in the sidebar search box to look across every library.</p>
      </section>
    );
  }

  const total = hits.authors.length + hits.books.length + hits.series.length;
  const authorLibrary = (a: Author): "ebook" | "audiobook" =>
    a.inEbookLibrary || !a.inAudiobookLibrary ? "ebook" : "audiobook";
  const bookLibrary = (b: Book): "ebook" | "audiobook" =>
    b.inEbookLibrary || !b.inAudiobookLibrary ? "ebook" : "audiobook";

  return (
    <>
      <section className="card">
        <h2>
          Search: “{query}” <span className="muted">({total} found)</span>
        </h2>
        {total === 0 && (
          <p className="muted">
            Nothing in your libraries matches. To add new content, use{" "}
            <strong>+ Add</strong> on a library page — it searches the metadata
            provider.
          </p>
        )}
      </section>

      {hits.authors.length > 0 && (
        <section className="card">
          <h2>Authors ({hits.authors.length})</h2>
          <div className="poster-grid">
            {hits.authors.map((a) => (
              <button
                key={a.id}
                className="poster-card"
                onClick={() => onOpenAuthor(a.id, authorLibrary(a))}
              >
                {a.imageUrl ? (
                  <img className="poster" src={proxiedImage(a.imageUrl)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{a.name.charAt(0)}</div>
                )}
                <span className="poster-title">{a.name}</span>
                <span className="poster-sub">
                  {[
                    a.inEbookLibrary ? "Ebooks" : "",
                    a.inAudiobookLibrary ? "Audiobooks" : "",
                  ]
                    .filter(Boolean)
                    .join(" · ") || "author"}
                </span>
              </button>
            ))}
          </div>
        </section>
      )}

      {hits.books.length > 0 && (
        <section className="card">
          <h2>Books ({hits.books.length})</h2>
          <div className="poster-grid">
            {hits.books.map(({ book: b, authorName }) => (
              <button
                key={b.id}
                className="poster-card"
                onClick={() => onOpenBook(b, bookLibrary(b))}
              >
                {b.coverUrl ? (
                  <img className="poster" src={proxiedImage(b.coverUrl)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{b.title.charAt(0)}</div>
                )}
                <span className="poster-title">{b.title}</span>
                <span className="poster-sub">
                  {authorName}
                  {b.releaseDate ? ` · ${b.releaseDate.slice(0, 4)}` : ""}
                </span>
              </button>
            ))}
          </div>
        </section>
      )}

      {hits.series.length > 0 && (
        <section className="card">
          <h2>Series & magazines ({hits.series.length})</h2>
          <div className="poster-grid">
            {hits.series.map((s) => (
              <button
                key={s.id}
                className="poster-card"
                onClick={() => onOpenSeries(s.id, s.mediaType)}
              >
                {s.coverUrl ? (
                  <img className="poster" src={proxiedImage(s.coverUrl)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{s.title.charAt(0)}</div>
                )}
                <span className="poster-title">{s.title}</span>
                <span className="poster-sub">{libraryLabels[s.mediaType] ?? s.mediaType}</span>
              </button>
            ))}
          </div>
        </section>
      )}
    </>
  );
}

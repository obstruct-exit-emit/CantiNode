import { useEffect, useState } from "react";
import { api, proxiedImage, type HomeItem, type HomeSection } from "../api";
import { libraryLabels } from "../App";
import { RowsSkeleton } from "../components/Skeleton";

// Home is the only place media types meet — as stacked per-library sections,
// never mixed within a row (Plex-style).
export default function HomeView({
  onError,
  onOpenLibrary,
  onOpenItem,
}: {
  onError: (message: string) => void;
  onOpenLibrary: (mediaType: string) => void;
  // Open a tile's own page: book detail for prose, series page for
  // volumes/issues (falls back to the library grid without the ids).
  onOpenItem: (mediaType: string, item: HomeItem) => void;
}) {
  const [sections, setSections] = useState<HomeSection[] | null>(null);

  useEffect(() => {
    api
      .home()
      .then(setSections)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!sections) return <RowsSkeleton rows={5} />;

  if (sections.length === 0) {
    return (
      <section className="card">
        <h2>Welcome to LibriNode</h2>
        <p className="muted">
          No libraries yet. Set one up under <strong>Settings → Root
          Folders</strong> — add a folder for ebooks, audiobooks, manga,
          comics, or magazines, and its library appears here.
        </p>
      </section>
    );
  }

  return (
    <>
      {sections.map((s) => (
        <section className="card" key={s.mediaType}>
          <div className="card-head">
            <h2>
              <button className="link" onClick={() => onOpenLibrary(s.mediaType)}>
                {libraryLabels[s.mediaType] ?? s.mediaType} ›
              </button>
            </h2>
            <span className="muted">
              {s.items} item{s.items === 1 ? "" : "s"}
              {s.mediaType !== "magazine" && s.wantedCount > 0 && ` · ${s.wantedCount} wanted`}
            </span>
          </div>
          {s.recentlyAdded.length > 0 && (
            <HomeRow
              title="Recently added"
              items={s.recentlyAdded}
              onOpen={(it) => onOpenItem(s.mediaType, it)}
            />
          )}
          {/* Magazines are organize-only for now — no acquisition, so no Wanted. */}
          {s.mediaType !== "magazine" && s.wanted.length > 0 && (
            <HomeRow
              title="Wanted"
              items={s.wanted}
              onOpen={(it) => onOpenItem(s.mediaType, it)}
            />
          )}
          {s.recentlyAdded.length === 0 && s.wanted.length === 0 && (
            <p className="muted">Library is empty — add something from its page.</p>
          )}
        </section>
      ))}
    </>
  );
}

function HomeRow({
  title,
  items,
  onOpen,
}: {
  title: string;
  items: HomeItem[];
  onOpen: (item: HomeItem) => void;
}) {
  return (
    <div className="home-row">
      <h3 className="muted">{title}</h3>
      <div className="home-strip">
        {items.map((it) => (
          <button
            className="home-tile"
            key={it.bookId}
            title={it.title}
            onClick={() => onOpen(it)}
          >
            {it.coverUrl ? (
              <img src={proxiedImage(it.coverUrl)} alt="" loading="lazy" />
            ) : (
              <div className="home-cover-fallback">{it.title.slice(0, 24)}</div>
            )}
            <div className="home-tile-title">{it.title}</div>
            {it.subtitle && <div className="home-tile-sub muted">{it.subtitle}</div>}
          </button>
        ))}
      </div>
    </div>
  );
}

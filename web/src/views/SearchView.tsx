import { useEffect, useMemo, useState } from "react";
import { api, proxiedImage, type MusicArtist } from "../api";
import { RowsSkeleton } from "../components/Skeleton";

// Global search: one query across the music library's artists, matched
// client-side against the artist list. This finds what you HAVE (and
// track); adding new artists stays on the library page, where the right
// MusicBrainz search is known.
export default function SearchView({
  query,
  onError,
  onOpenArtist,
}: {
  query: string;
  onError: (message: string) => void;
  onOpenArtist: (id: number) => void;
}) {
  const [artists, setArtists] = useState<MusicArtist[] | null>(null);

  useEffect(() => {
    api
      .listMusicArtists()
      .then(setArtists)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  const q = query.trim().toLowerCase();
  const hits = useMemo(() => {
    if (!artists || q === "") return null;
    return {
      artists: artists.filter((ar) => ar.name.toLowerCase().includes(q)).slice(0, 24),
    };
  }, [artists, q]);

  if (!artists) return <RowsSkeleton rows={5} />;
  if (!hits) {
    return (
      <section className="card">
        <h2>Search</h2>
        <p className="muted">Type in the sidebar search box to look across your library.</p>
      </section>
    );
  }

  const total = hits.artists.length;

  return (
    <>
      <section className="card">
        <h2>
          Search: “{query}” <span className="muted">({total} found)</span>
        </h2>
        {total === 0 && (
          <p className="muted">
            Nothing in your library matches. To add a new artist, use{" "}
            <strong>+ Add</strong> on the Music page — it searches MusicBrainz.
          </p>
        )}
      </section>

      {hits.artists.length > 0 && (
        <section className="card">
          <h2>Music Artists ({hits.artists.length})</h2>
          <div className="poster-grid">
            {hits.artists.map((a) => (
              <button key={a.id} className="poster-card" onClick={() => onOpenArtist(a.id)}>
                {a.imageUrl ? (
                  <img className="poster" src={proxiedImage(a.imageUrl)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{a.name.charAt(0)}</div>
                )}
                <span className="poster-title">{a.name}</span>
                <span className="poster-sub">{a.isMonitored ? "monitored" : "artist"}</span>
              </button>
            ))}
          </div>
        </section>
      )}
    </>
  );
}

import { useEffect, useMemo, useState } from "react";
import { api, proxiedImage, type MusicArtist, type TrackSearchResult } from "../api";
import AddToPlaylistModal from "../components/AddToPlaylistModal";
import { RowsSkeleton } from "../components/Skeleton";
import { formatDuration } from "../format";

// Global search: artists matched client-side against the full artist list
// (small, already loaded elsewhere); tracks matched server-side (owned
// tracks can run into the thousands, so this one's a real query, not a
// client-side filter) — both find what you HAVE. Adding new artists stays
// on the library page, where the right MusicBrainz search is known.
export default function SearchView({
  query,
  onError,
  onOpenArtist,
  onOpenAlbum,
}: {
  query: string;
  onError: (message: string) => void;
  onOpenArtist: (id: number) => void;
  onOpenAlbum: (albumId: number, artistId: number) => void;
}) {
  const [artists, setArtists] = useState<MusicArtist[] | null>(null);
  const [tracks, setTracks] = useState<TrackSearchResult[] | null>(null);
  const [addToPlaylist, setAddToPlaylist] = useState<{ label: string; trackIds: number[] } | null>(null);

  useEffect(() => {
    api
      .listMusicArtists()
      .then(setArtists)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  const q = query.trim();
  // Debounced so each keystroke doesn't hit the API — same pattern
  // ActivityView's history filter already uses.
  useEffect(() => {
    if (q === "") {
      setTracks(null);
      return;
    }
    const t = window.setTimeout(() => {
      api.searchOwnedTracks(q).then(setTracks).catch(() => setTracks([]));
    }, 250);
    return () => window.clearTimeout(t);
  }, [q]);

  const qLower = q.toLowerCase();
  const hits = useMemo(() => {
    if (!artists || qLower === "") return null;
    return {
      artists: artists.filter((ar) => ar.name.toLowerCase().includes(qLower)).slice(0, 24),
    };
  }, [artists, qLower]);

  if (!artists) return <RowsSkeleton rows={5} />;
  if (!hits) {
    return (
      <section className="card">
        <h2>Search</h2>
        <p className="muted">Type in the sidebar search box to look across your library.</p>
      </section>
    );
  }

  const total = hits.artists.length + (tracks?.length ?? 0);

  return (
    <>
      <section className="card">
        <h2>
          Search: “{query}” <span className="muted">({total} found)</span>
        </h2>
        {total === 0 && tracks !== null && (
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

      {tracks && tracks.length > 0 && (
        <section className="card">
          <h2>Tracks ({tracks.length})</h2>
          <ul className="rows">
            {tracks.map((t) => (
              <li key={t.trackId}>
                <div className="row">
                  <span>
                    {t.title}
                    <span className="muted">
                      {" — "}
                      <button className="link" onClick={() => onOpenArtist(t.artistId)}>
                        {t.artistName}
                      </button>
                      {" · "}
                      <button className="link" onClick={() => onOpenAlbum(t.albumId, t.artistId)}>
                        {t.albumTitle}
                      </button>
                    </span>
                  </span>
                  <span className="row-actions">
                    <span className="muted">{formatDuration(t.durationMs)}</span>
                    <button
                      className="toggle"
                      title="Add this track to a playlist"
                      onClick={() => setAddToPlaylist({ label: t.title, trackIds: [t.trackId] })}
                    >
                      + playlist
                    </button>
                  </span>
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {addToPlaylist && (
        <AddToPlaylistModal
          itemLabel={addToPlaylist.label}
          onAdd={(playlistId) =>
            api.addPlaylistItemsBulk(playlistId, addToPlaylist.trackIds).then(() => {})
          }
          onClose={() => setAddToPlaylist(null)}
        />
      )}
    </>
  );
}

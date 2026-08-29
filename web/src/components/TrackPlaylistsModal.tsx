import { useEffect, useState } from "react";
import { api, type Playlist } from "../api";

// TrackPlaylistsModal lists which playlist(s) a track belongs to — opened
// from the album track list's "in playlist" badge, the same way
// TrackCreditsModal opens from "Featuring". Each entry jumps straight to
// that playlist's own detail page.
export default function TrackPlaylistsModal({
  trackTitle,
  trackId,
  onOpenPlaylist,
  onClose,
}: {
  trackTitle: string;
  trackId: number;
  onOpenPlaylist: (id: number) => void;
  onClose: () => void;
}) {
  const [playlists, setPlaylists] = useState<Playlist[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .listPlaylistsForTrack(trackId)
      .then(setPlaylists)
      .catch((err: unknown) => setError(String(err instanceof Error ? err.message : err)));
  }, [trackId]);

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3>In playlists</h3>
        <p className="muted tag-modal-filename" title={trackTitle}>
          {trackTitle}
        </p>
        {error && <p className="notice bad">{error}</p>}
        {!error && !playlists && <p className="muted">Loading…</p>}
        {playlists && playlists.length === 0 && <p className="muted">Not in any playlist.</p>}
        {playlists && playlists.length > 0 && (
          <ul className="credits-list">
            {playlists.map((p) => (
              <li key={p.id}>
                <span>{p.name}</span>
                <button className="toggle" onClick={() => onOpenPlaylist(p.id)}>
                  Open ↗
                </button>
              </li>
            ))}
          </ul>
        )}
        <div className="settings-actions">
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}

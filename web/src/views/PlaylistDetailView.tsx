import { useCallback, useEffect, useState } from "react";
import { api, type PlaylistDetail, type PlaylistTrack } from "../api";
import { formatDuration } from "../format";
import { RowsSkeleton } from "../components/Skeleton";
import { useUi } from "../ui";

export default function PlaylistDetailView({
  id,
  onError,
  onBack,
  onOpenArtist,
  onOpenAlbum,
}: {
  id: number;
  onError: (message: string) => void;
  onBack: () => void;
  onOpenArtist: (id: number) => void;
  onOpenAlbum: (id: number, artistId: number) => void;
}) {
  const { confirmDlg } = useUi();
  const [playlist, setPlaylist] = useState<PlaylistDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dragOverIndex, setDragOverIndex] = useState<number | null>(null);

  const reload = useCallback(() => {
    api
      .getPlaylist(id)
      .then((p) => {
        setPlaylist(p);
        setName(p.name);
        setDescription(p.description);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [id, onError]);

  useEffect(reload, [reload]);

  const saveEdit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    api
      .updatePlaylist(id, name.trim(), description.trim())
      .then(() => {
        setEditing(false);
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const removeTrack = (t: PlaylistTrack) => {
    api
      .removePlaylistItem(id, t.itemId)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  const reorderTo = (from: number, to: number) => {
    if (!playlist || from === to) return;
    const order = playlist.tracks.map((t) => t.itemId);
    const [moved] = order.splice(from, 1);
    order.splice(to, 0, moved);
    api
      .reorderPlaylistItems(id, order)
      .then((tracks) => setPlaylist({ ...playlist, tracks }))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  const move = (index: number, dir: -1 | 1) => {
    if (!playlist) return;
    const target = index + dir;
    if (target < 0 || target >= playlist.tracks.length) return;
    reorderTo(index, target);
  };

  const exportM3U = () => {
    api
      .exportPlaylist(id)
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `${playlist?.name ?? "playlist"}.m3u`;
        a.click();
        URL.revokeObjectURL(url);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  const deletePlaylist = async () => {
    if (!playlist) return;
    const ok = await confirmDlg({
      title: "Delete playlist",
      message: `Delete "${playlist.name}"? Its tracks stay in your library untouched — only the playlist itself goes away.`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    api
      .deletePlaylist(id)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  if (loading) return <RowsSkeleton />;
  if (!playlist) return null;

  return (
    <section className="card">
      <div className="card-head">
        <span>
          <button className="link" onClick={onBack}>
            ← Playlists
          </button>
        </span>
        <span className="row-actions">
          <button onClick={() => setEditing(!editing)}>{editing ? "Cancel" : "Edit"}</button>
          <button
            disabled={playlist.tracks.every((t) => !t.trackFileId)}
            title="Download as an M3U file, playable in any real music player pointed at this library"
            onClick={exportM3U}
          >
            Export M3U
          </button>
          <button className="danger" onClick={deletePlaylist}>
            delete playlist
          </button>
        </span>
      </div>

      {editing ? (
        <form onSubmit={saveEdit}>
          <input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          <input
            placeholder="Description (optional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
          <button type="submit" disabled={busy || !name.trim()}>
            Save
          </button>
        </form>
      ) : (
        <>
          <h2>{playlist.name}</h2>
          {playlist.description && <p className="muted">{playlist.description}</p>}
        </>
      )}

      <p className="muted">
        {playlist.tracks.length} track{playlist.tracks.length === 1 ? "" : "s"}
        {playlist.totalDurationMs > 0 ? ` · ${formatDuration(playlist.totalDurationMs)}` : ""}
      </p>

      {playlist.tracks.length === 0 ? (
        <p className="muted">
          No tracks yet — add some from any album's track list.
        </p>
      ) : (
        <ul className="rows">
          {playlist.tracks.map((t, i) => (
            <li
              key={t.itemId}
              draggable
              onDragStart={() => setDragIndex(i)}
              onDragOver={(e) => {
                e.preventDefault();
                if (dragOverIndex !== i) setDragOverIndex(i);
              }}
              onDragLeave={() => setDragOverIndex((cur) => (cur === i ? null : cur))}
              onDrop={(e) => {
                e.preventDefault();
                if (dragIndex !== null) reorderTo(dragIndex, i);
                setDragIndex(null);
                setDragOverIndex(null);
              }}
              onDragEnd={() => {
                setDragIndex(null);
                setDragOverIndex(null);
              }}
              style={{
                opacity: dragIndex === i ? 0.5 : 1,
                borderTop: dragOverIndex === i && dragIndex !== i ? "2px solid var(--accent)" : undefined,
                cursor: "grab",
              }}
            >
              <div className="row">
                <span>
                  {!t.trackFileId && (
                    <span className="pill off" title="No file currently matched to this track">
                      missing
                    </span>
                  )}{" "}
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
                  <button disabled={i === 0} title="Move up" onClick={() => move(i, -1)}>
                    ▲
                  </button>
                  <button
                    disabled={i === playlist.tracks.length - 1}
                    title="Move down"
                    onClick={() => move(i, 1)}
                  >
                    ▼
                  </button>
                  <button className="danger" onClick={() => removeTrack(t)}>
                    remove
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

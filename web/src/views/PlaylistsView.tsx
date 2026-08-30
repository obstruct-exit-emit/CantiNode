import { useCallback, useEffect, useState } from "react";
import { api, type Playlist } from "../api";
import { formatDuration } from "../format";
import { RowsSkeleton } from "../components/Skeleton";
import { RowMenu, useUi } from "../ui";

export default function PlaylistsView({
  onError,
  onOpenPlaylist,
}: {
  onError: (message: string) => void;
  onOpenPlaylist: (id: number) => void;
}) {
  const { confirmDlg } = useUi();
  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [loading, setLoading] = useState(true);
  const [showNew, setShowNew] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    api
      .listPlaylists()
      .then(setPlaylists)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError]);

  useEffect(reload, [reload]);

  const create = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    api
      .createPlaylist(name.trim(), description.trim())
      .then((p) => {
        setName("");
        setDescription("");
        setShowNew(false);
        reload();
        onOpenPlaylist(p.id);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const remove = async (p: Playlist) => {
    const ok = await confirmDlg({
      title: "Delete playlist",
      message: `Delete "${p.name}"? Its ${p.trackCount} track${p.trackCount === 1 ? "" : "s"} stay in your library untouched — only the playlist itself goes away.`,
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    api
      .deletePlaylist(p.id)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  return (
    <>
      <section className="card card-tight">
        <div className="card-head">
          <h2>Playlists ({playlists.length})</h2>
          <span className="row-actions">
            <button onClick={() => setShowNew(!showNew)}>{showNew ? "Close" : "+ New"}</button>
          </span>
        </div>
        {showNew && (
          <form onSubmit={create}>
            <input
              placeholder="Playlist name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
            <input
              placeholder="Description (optional)"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
            <button type="submit" disabled={busy || !name.trim()}>
              Create
            </button>
          </form>
        )}
      </section>
      <section className="card card-flush-top">
        {loading ? (
          <RowsSkeleton />
        ) : playlists.length === 0 ? (
          <p className="muted">
            No playlists yet. Create one, then add tracks to it from any album
            page.
          </p>
        ) : (
          <ul className="rows playlist-rows">
            {playlists.map((p) => (
              <li key={p.id}>
                <div className="row">
                  <button className="playlist-open" onClick={() => onOpenPlaylist(p.id)}>
                    <span className="playlist-icon" aria-hidden="true">
                      ♪
                    </span>
                    <span className="playlist-info">
                      <span className="playlist-name">{p.name}</span>
                      {p.description && <span className="playlist-desc muted">{p.description}</span>}
                    </span>
                  </button>
                  <span className="row-actions">
                    <span className="pill playlist-meta">
                      {p.trackCount} track{p.trackCount === 1 ? "" : "s"}
                      {p.totalDurationMs > 0 ? ` · ${formatDuration(p.totalDurationMs)}` : ""}
                    </span>
                    <RowMenu
                      items={[
                        {
                          label: "Delete",
                          title: "Delete this playlist",
                          danger: true,
                          onSelect: () => remove(p),
                        },
                      ]}
                    />
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}

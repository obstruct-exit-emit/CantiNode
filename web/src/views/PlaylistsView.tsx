import { useCallback, useEffect, useRef, useState } from "react";
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
  const [importNotice, setImportNotice] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);

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

  const importM3U = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // let the same file be re-picked later
    if (!file) return;
    setImportNotice("");
    const reader = new FileReader();
    reader.onload = () => {
      const content = String(reader.result ?? "");
      const defaultName = file.name.replace(/\.m3u8?$/i, "");
      api
        .importPlaylist(defaultName, content)
        .then((result) => {
          setImportNotice(
            `Imported "${result.playlist.name}": ${result.imported} track${result.imported === 1 ? "" : "s"} added` +
              (result.skipped > 0 ? `, ${result.skipped} skipped (not in this library)` : "") +
              ".",
          );
          reload();
        })
        .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
    };
    reader.onerror = () => onError("Couldn't read that file.");
    reader.readAsText(file);
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
            <input
              ref={fileInput}
              type="file"
              accept=".m3u,.m3u8"
              style={{ display: "none" }}
              onChange={importM3U}
            />
            <button
              title="Import an M3U file as a new playlist — a line that doesn't match any file currently in this library is skipped"
              onClick={() => fileInput.current?.click()}
            >
              Import M3U
            </button>
            <button onClick={() => setShowNew(!showNew)}>{showNew ? "Close" : "+ New"}</button>
          </span>
        </div>
        {importNotice && <p className="muted">{importNotice}</p>}
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
      <section className="card">
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

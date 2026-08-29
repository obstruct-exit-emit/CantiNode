import { useEffect, useState } from "react";
import { api, type Playlist } from "../api";

// AddToPlaylistModal: a track's own "+ playlist" action — pick an existing
// playlist or create one on the spot, same one-step flow either way.
export default function AddToPlaylistModal({
  itemLabel,
  onAdd,
  onClose,
}: {
  // What's being added — a track's title, or "12 tracks" for a whole album.
  itemLabel: string;
  onAdd: (playlistId: number) => Promise<void>;
  onClose: () => void;
}) {
  const [playlists, setPlaylists] = useState<Playlist[] | null>(null);
  const [selected, setSelected] = useState<number | "new">("new");
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .listPlaylists()
      .then((list) => {
        setPlaylists(list);
        if (list.length > 0) setSelected(list[0].id);
      })
      .catch(() => setPlaylists([]));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setNotice("");
    try {
      let playlistId = typeof selected === "number" ? selected : 0;
      if (selected === "new") {
        if (!newName.trim()) {
          setBusy(false);
          return;
        }
        const p = await api.createPlaylist(newName.trim(), "");
        playlistId = p.id;
      }
      await onAdd(playlistId);
      onClose();
    } catch (err: unknown) {
      setNotice(String(err instanceof Error ? err.message : err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3>Add to playlist</h3>
        <p className="muted">{itemLabel}</p>
        {playlists === null ? (
          <p className="muted">Loading…</p>
        ) : (
          <form onSubmit={submit}>
            {playlists.length > 0 && (
              <label>
                <input
                  type="radio"
                  name="playlist-choice"
                  checked={selected !== "new"}
                  onChange={() => setSelected(playlists[0].id)}
                />{" "}
                <select
                  disabled={selected === "new"}
                  value={typeof selected === "number" ? selected : ""}
                  onChange={(e) => setSelected(Number(e.target.value))}
                >
                  {playlists.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name} ({p.trackCount})
                    </option>
                  ))}
                </select>
              </label>
            )}
            <label>
              <input
                type="radio"
                name="playlist-choice"
                checked={selected === "new"}
                onChange={() => setSelected("new")}
              />{" "}
              <input
                placeholder="New playlist name"
                value={newName}
                onFocus={() => setSelected("new")}
                onChange={(e) => setNewName(e.target.value)}
              />
            </label>
            {notice && <p className="notice bad">{notice}</p>}
            <div className="settings-actions">
              <button type="submit" disabled={busy || (selected === "new" && !newName.trim())}>
                Add
              </button>
              <button type="button" className="toggle" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}

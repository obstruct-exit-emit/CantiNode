import { useEffect, useRef, useState } from "react";
import { api, type FolderListing } from "../api";

// FolderBrowser is the visual picker behind "Browse…" on root-folder forms:
// walk the server's filesystem (folders only), click into subfolders, go up,
// or type a path directly — then choose the folder you're standing in.
export default function FolderBrowser({
  initial,
  onPick,
  onClose,
}: {
  initial?: string;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const [listing, setListing] = useState<FolderListing | null>(null);
  const [input, setInput] = useState(initial ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // requestToken guards against an out-of-order response: every button that
  // triggers a new load() is already disabled while busy, but the input's
  // own Enter-key handler below isn't (typing a new path must stay live
  // while a previous lookup is still in flight) — pressing Enter twice in
  // quick succession, or pressing Enter and then immediately clicking a
  // folder before the next render disables it, can start a second load()
  // before the first's response arrives. Only the most recently started
  // call's response is ever applied, the same pattern already used for
  // UnmatchedFilesView's AutoMatchPanel — otherwise a slower, older
  // response arriving after a newer one can silently overwrite the path
  // the user actually navigated to, which "Choose this folder" would then
  // submit as a root folder's location.
  const requestToken = useRef(0);

  const load = (p?: string) => {
    const token = ++requestToken.current;
    setBusy(true);
    setError("");
    api
      .browseFolders(p)
      .then((l) => {
        if (requestToken.current !== token) return;
        setListing(l);
        setInput(l.path);
      })
      .catch((err: unknown) => {
        if (requestToken.current !== token) return;
        setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (requestToken.current === token) setBusy(false);
      });
  };

  // Start at the given path; fall back to the filesystem root when it's
  // empty or unreadable.
  useEffect(() => {
    const start = (initial ?? "").trim();
    if (!start) {
      load(undefined);
      return;
    }
    const token = ++requestToken.current;
    setBusy(true);
    api
      .browseFolders(start)
      .then((l) => {
        if (requestToken.current !== token) return;
        setListing(l);
        setInput(l.path);
      })
      .catch(() => load(undefined))
      .finally(() => {
        if (requestToken.current === token) setBusy(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="folder-browser">
      <div className="fb-path">
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") load(input.trim() || undefined);
          }}
          placeholder="/"
        />
        <button className="toggle" disabled={busy} onClick={() => load(input.trim() || undefined)}>
          Go
        </button>
      </div>
      {error && <p className="notice bad fb-error">{error}</p>}
      <ul className="fb-list">
        {listing && listing.parent !== listing.path && (listing.parent !== "" || listing.path !== "") && (
          <li>
            <button className="link" disabled={busy} onClick={() => load(listing.parent || undefined)}>
              ⬆️ ..
            </button>
          </li>
        )}
        {listing?.directories.map((d) => (
          <li key={d.path}>
            <button className="link" disabled={busy} onClick={() => load(d.path)}>
              📁 {d.name}
            </button>
          </li>
        ))}
        {listing && listing.directories.length === 0 && (
          <li className="muted fb-empty">No subfolders</li>
        )}
      </ul>
      <div className="settings-actions fb-actions">
        <button
          disabled={busy || !listing || listing.path === ""}
          onClick={() => listing && onPick(listing.path)}
          title="Use the folder shown above"
        >
          Choose this folder
        </button>
        <button className="toggle" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  );
}

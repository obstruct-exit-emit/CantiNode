import { useCallback, useEffect, useState } from "react";
import { api, type BlockEntry, type GrabRecord, type QueueItem } from "../api";
import { relativeTime } from "../format";
import { RowsSkeleton } from "../components/Skeleton";
import { useUi } from "../ui";
import ReleaseBrowser from "../components/ReleaseBrowser";

export default function ActivityView({
  onError,
}: {
  onError: (message: string) => void;
}) {
  const { confirmDlg } = useUi();
  const [items, setItems] = useState<QueueItem[]>([]);
  const [history, setHistory] = useState<GrabRecord[]>([]);
  const [histTotal, setHistTotal] = useState(0);
  const [histFilter, setHistFilter] = useState("");
  const [histLimit, setHistLimit] = useState(100);
  const [blocked, setBlocked] = useState<BlockEntry[]>([]);
  const [clientErrors, setClientErrors] = useState<string[]>([]);
  // Loaded independently, not gated behind Promise.all: the queue call polls
  // live download clients and can run seconds behind history/blocklist (a
  // slow or debrid-bridged client's queue answers far slower than a plain DB
  // read) — no reason to blank out the whole page waiting on the slowest of
  // the three.
  const [itemsLoading, setItemsLoading] = useState(true);
  const [removing, setRemoving] = useState("");
  const [retryGrabId, setRetryGrabId] = useState<number | null>(null);

  const reload = useCallback(() => {
    api
      .queue()
      .then((q) => {
        setItems(q.items);
        setClientErrors(q.errors);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setItemsLoading(false));
    api
      .history(histFilter, histLimit)
      .then((h) => {
        setHistory(h.records);
        setHistTotal(h.total);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
    api
      .blocklist()
      .then(setBlocked)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError, histFilter, histLimit]);

  // The filter re-queries as you type — debounced so each keystroke doesn't
  // hit the API.
  useEffect(() => {
    const t = window.setTimeout(() => {
      api
        .history(histFilter, histLimit)
        .then((h) => {
          setHistory(h.records);
          setHistTotal(h.total);
        })
        .catch(() => {});
    }, 250);
    return () => window.clearTimeout(t);
  }, [histFilter, histLimit]);

  const removeItem = async (it: QueueItem) => {
    const ok = await confirmDlg({
      title: "Remove download",
      message: `Remove "${it.title}" from ${it.client}?\n\nIts downloaded data is deleted. The release is NOT blocklisted, so it can be grabbed again.`,
      confirmLabel: "Remove download",
      danger: true,
    });
    if (!ok) return;
    setRemoving(it.id);
    api
      .removeQueueItem(it.clientConfigId, it.id, it.grabId)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setRemoving(""));
  };

  const cancelGrab = async (g: GrabRecord) => {
    const ok = await confirmDlg({
      title: "Cancel pending grab",
      message: `Stop tracking "${g.title}" as pending?\n\nThis only clears CantiNode's own record — it does not touch the download client. Use this when the download is gone from Activity above but a new search still says one is already pending.`,
      confirmLabel: "Cancel grab",
      danger: true,
    });
    if (!ok) return;
    api
      .cancelGrab(g.id)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  // Poll while the tab is open; downloads move fast.
  useEffect(() => {
    reload();
    const timer = setInterval(reload, 10_000);
    return () => clearInterval(timer);
  }, [reload]);

  return (
    <>
    <section className="card">
      <div className="card-head">
        <h2>Queue ({items.length})</h2>
        <span className="row-actions">
          <button onClick={reload}>Refresh</button>
        </span>
      </div>
      {clientErrors.map((e) => (
        <p key={e} className="notice bad">
          {e}
        </p>
      ))}
      {itemsLoading ? (
        <RowsSkeleton />
      ) : items.length === 0 ? (
        <p className="muted">
          Nothing downloading. Grab releases from a book's search results, or
          check that a download client is configured under Settings.
        </p>
      ) : (
        <ul className="rows">
          {items.map((it) => (
            <li key={it.client + it.id + it.title}>
              <div className="row">
                <span>
                  {it.title}
                  {it.path && <span className="file-path muted"> → {it.path}</span>}
                </span>
                <span className="row-actions">
                  <span className="muted">{it.client}</span>
                  <span className={`owned ${it.status === "failed" ? "no" : "yes"}`}>
                    {it.status}
                    {it.status === "downloading" &&
                      ` ${(it.progress * 100).toFixed(0)}%`}
                  </span>
                  <button
                    className="danger"
                    disabled={removing === it.id}
                    title="Remove this download from the client (its files are deleted; the release is not blocklisted)"
                    onClick={() => removeItem(it)}
                  >
                    remove
                  </button>
                </span>
              </div>
              <div className="progress" title={`${(it.progress * 100).toFixed(0)}%`}>
                <div
                  className={`progress-fill${it.status === "failed" ? " bad" : ""}${it.status === "completed" || it.status === "seeded" ? " done" : ""}`}
                  style={{ width: `${Math.max(2, Math.min(100, it.progress * 100))}%` }}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>

    {blocked.length > 0 && (
      <section className="card">
        <details className="disclosure">
          <summary>Blocklist ({blocked.length})</summary>
          <div className="disclosure-body">
            <p className="muted" style={{ margin: 0 }}>
              Releases that failed to download — never grabbed again. Remove an
              entry to give a release another chance.
            </p>
            <ul className="rows">
              {blocked.map((b) => (
                <li key={b.id}>
                  <div className="row">
                    <span>
                      {b.title}
                      {b.reason && <span className="file-path muted"> — {b.reason}</span>}
                    </span>
                    <span className="row-actions">
                      <span className="muted" title={b.blockedAt}>
                        {relativeTime(b.blockedAt)}
                      </span>
                      <button
                        className="toggle"
                        onClick={() =>
                          api
                            .unblock(b.id)
                            .then(reload)
                            .catch((err: unknown) =>
                              onError(String(err instanceof Error ? err.message : err)),
                            )
                        }
                      >
                        remove
                      </button>
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        </details>
      </section>
    )}

    {(histTotal > 0 || histFilter) && (
      <section className="card">
        <details className="disclosure">
          <summary>History ({histTotal})</summary>
          <div className="disclosure-body">
            <input
              className="grid-filter"
              placeholder="Filter history by title…"
              value={histFilter}
              onChange={(e) => {
                setHistFilter(e.target.value);
                setHistLimit(100);
              }}
            />
            {history.length === 0 && (
              <p className="muted">No grabs match the filter.</p>
            )}
            <ul className="rows">
              {history.map((g) => {
                // A failed grab tied to a wanted album or an owned-album
                // upgrade can search again right here — same release list
                // "Search releases"/"Search upgrade" opens from the album
                // page, without navigating away from Activity first. A
                // grab with neither (shouldn't happen for a music grab,
                // but the data doesn't guarantee it) has nothing to retry
                // against.
                const canRetry = g.status === "failed" && (!!g.wantedAlbumId || !!g.upgradeAlbumId);
                return (
                  <li key={g.id}>
                    <div className="row">
                      <span>
                        {g.title}
                        {g.message && <span className="file-path muted"> — {g.message}</span>}
                      </span>
                      <span className="row-actions">
                        <span className="muted" title={g.grabbedAt}>
                          {relativeTime(g.grabbedAt)}
                        </span>
                        <span className="muted">{g.protocol}</span>
                        <span className={`owned ${g.status === "failed" ? "no" : "yes"}`}>
                          {g.status}
                        </span>
                        {g.status === "grabbed" && (
                          <button
                            className="danger"
                            title="Clear this pending grab without touching the download client — use this when the download itself is already gone but a new search still says one is pending"
                            onClick={() => cancelGrab(g)}
                          >
                            cancel
                          </button>
                        )}
                        {canRetry && (
                          <button
                            className={retryGrabId === g.id ? "toggle on" : "toggle"}
                            title="Search for a different release"
                            onClick={() => setRetryGrabId(retryGrabId === g.id ? null : g.id)}
                          >
                            {retryGrabId === g.id ? "Hide releases" : "Search again"}
                          </button>
                        )}
                      </span>
                    </div>
                    {retryGrabId === g.id && g.wantedAlbumId ? (
                      <ReleaseBrowser
                        wantedAlbumId={g.wantedAlbumId}
                        onGrabbed={() => {
                          setRetryGrabId(null);
                          reload();
                        }}
                        onClose={() => setRetryGrabId(null)}
                      />
                    ) : retryGrabId === g.id && g.upgradeAlbumId ? (
                      <ReleaseBrowser
                        upgradeAlbumId={g.upgradeAlbumId}
                        onGrabbed={() => {
                          setRetryGrabId(null);
                          reload();
                        }}
                        onClose={() => setRetryGrabId(null)}
                      />
                    ) : null}
                  </li>
                );
              })}
            </ul>
            {history.length < histTotal && (
              <button
                className="toggle show-more"
                onClick={() => setHistLimit(histLimit + 200)}
              >
                Show more ({history.length} of {histTotal})
              </button>
            )}
          </div>
        </details>
      </section>
    )}
    </>
  );
}

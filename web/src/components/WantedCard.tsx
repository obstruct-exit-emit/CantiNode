import { useCallback, useEffect, useState } from "react";
import { api, type HomeItem } from "../api";
import { downloadPct, useQueue } from "../useQueue";
import { SortSelect, DirectionButtons, sortItems, groupBySeries, defaultDirFor, type SortDir } from "./SortControl";

// WantedCard is the per-library Wanted page: everything monitored but
// missing this format's file, each with its own search button.
export default function WantedCard({
  library,
  onError,
}: {
  library: string;
  onError: (message: string) => void;
}) {
  const [items, setItems] = useState<HomeItem[]>([]);
  const [busyID, setBusyID] = useState<number | null>(null);
  const [notice, setNotice] = useState("");
  const [sort, setSort] = useState("default");
  const [dir, setDir] = useState<SortDir>(defaultDirFor("default"));
  // Picking a new sort key starts at that key's own natural direction; the
  // direction dropdown then applies to whichever key is currently chosen.
  const changeSort = (key: string) => {
    setSort(key);
    setDir(defaultDirFor(key));
  };
  // Shared queue poll: wanted rows with an active download show its progress
  // instead of another grab button.
  const queue = useQueue();

  const reload = useCallback(() => {
    api
      .wanted(library)
      .then((r) => setItems(r.items))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [library, onError]);

  useEffect(reload, [reload]);

  if (items.length === 0) return null;

  const grab = (item: HomeItem) => {
    setBusyID(item.bookId);
    setNotice("");
    api
      .autoSearchBook(item.bookId, library)
      .then((o) => {
        if (o.grabbed) {
          setNotice(`✓ Grabbed "${o.release}" → ${o.client}`);
          queue.refresh(); // show the downloading state right away
        } else {
          setNotice(`✗ ${item.title}: ${o.message ?? "nothing grabbed"}`);
        }
        reload();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusyID(null));
  };

  const shown = sortItems(items, sort, dir);

  const renderRow = (item: HomeItem) => (
    <li key={item.bookId}>
      <div className="row">
        <span>
          {item.title}
          {item.subtitle && <span className="muted"> · {item.subtitle}</span>}
        </span>
        <span className="row-actions">
          {(() => {
            const dl = queue.activeFor(item.bookId, library);
            return dl ? (
              <span className="owned dl" title={`${dl.status} on ${dl.client}`}>
                ⬇ downloading {downloadPct(dl)}
              </span>
            ) : (
              <button disabled={busyID !== null} onClick={() => grab(item)}>
                {busyID === item.bookId ? "Searching…" : "Auto grab"}
              </button>
            );
          })()}
        </span>
      </div>
    </li>
  );

  return (
    <section className="card">
      <div className="card-head">
        <h2>Wanted ({items.length})</h2>
        <SortSelect
          value={sort}
          onChange={changeSort}
          options={[
            ["default", "Recently added"],
            ["title", "Title"],
            ["series", "Series"],
            ["date", "Release date"],
            ["rating", "Rating"],
          ]}
        />
        <DirectionButtons value={dir} onChange={setDir} />
      </div>
      <p className="muted">
        Monitored but not owned in this library. Auto grab searches the
        indexers and sends the best release to a download client.
      </p>
      {notice && <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>}
      {sort === "series" ? (
        groupBySeries(shown, (i) => i.seriesTitle ?? "").map((g, _gi, arr) => (
          <div key={g.title || "standalone"}>
            {(g.title || arr.some((x) => x.title !== "")) && (
              <h3 className="group-heading">{g.title || "Standalone"}</h3>
            )}
            <ul className="rows">{g.items.map(renderRow)}</ul>
          </div>
        ))
      ) : (
        <ul className="rows">{shown.map(renderRow)}</ul>
      )}
    </section>
  );
}

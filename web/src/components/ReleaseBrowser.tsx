import { useEffect, useMemo, useState } from "react";
import { api, type ReleaseCandidate } from "../api";
import { formatBytes } from "../format";

// ReleaseBrowser is the interactive search: every release candidate for a
// book/volume, scored and organized — approved first, sortable, filterable by
// protocol, rejected ones dimmed with their reasons and still force-grabbable.
// Shared by the book page and series volume rows so search behaves (and looks)
// the same in every library.

type SortKey = "score" | "size" | "seeders" | "age";
type ProtoFilter = "all" | "usenet" | "torrent" | "direct";

// "direct" covers any non-P2P, non-Newznab source that hands back a plain
// downloadable file (Library Genesis today; deliberately not named after any
// one indexer, so a future direct-download source falls under it for free).
const protoIcon = (p: string) => (p === "usenet" ? "📡" : p === "direct" ? "⬇️" : "🧲");

function fmtAge(publishDate?: string): string {
  if (!publishDate) return "";
  const ms = Date.now() - new Date(publishDate).getTime();
  if (isNaN(ms) || ms < 0) return "";
  const days = ms / 86_400_000;
  if (days < 1) return "today";
  if (days < 30) return `${Math.floor(days)}d`;
  if (days < 365) return `${Math.floor(days / 30)}mo`;
  return `${Math.floor(days / 365)}y`;
}

function ageValue(publishDate?: string): number {
  const t = publishDate ? new Date(publishDate).getTime() : 0;
  return isNaN(t) ? 0 : t;
}

// leechers derives from the Torznab convention peers = seeders + leechers;
// indexers that report leechers directly (peers < seeders) pass through.
function leechers(c: ReleaseCandidate): number | null {
  if (c.protocol !== "torrent" || c.peers < 0) return null;
  return c.seeders >= 0 && c.peers >= c.seeders ? c.peers - c.seeders : c.peers;
}

// isPack reports whether the release's parsed title looks like it bundles
// multiple books/volumes: an explicit volume span ("v01-v12") or a
// self-declared complete run/collection even without one.
function isPack(c: ReleaseCandidate): boolean {
  return c.parsed.pack === true || (c.parsed.volumeEnd ?? 0) > (c.parsed.volume ?? 0);
}

export default function ReleaseBrowser({
  bookId,
  mediaType,
  packSeriesId,
  onGrabbed,
  onClose,
}: {
  // The wanted book/volume — or, in pack mode, unused (grabs bind to the
  // grabBookId the pack search returns).
  bookId?: number;
  mediaType: string;
  // Series pack mode: search whole-series packs for this series instead of
  // single releases for one book.
  packSeriesId?: number;
  // Called after a release was sent to a client — refresh queue/badges.
  onGrabbed?: () => void;
  onClose?: () => void;
}) {
  const [releases, setReleases] = useState<ReleaseCandidate[] | null>(null);
  const [errors, setErrors] = useState<string[]>([]);
  const [loadError, setLoadError] = useState("");
  const [grabBookId, setGrabBookId] = useState(0);
  const [showRejected, setShowRejected] = useState(false);
  const [proto, setProto] = useState<ProtoFilter>("all");
  const [sort, setSort] = useState<SortKey>("score");
  // Per-release grab state, keyed by guid+indexer: "sending", "✓ …", "✗ …".
  const [grabState, setGrabState] = useState<Record<string, string>>({});

  useEffect(() => {
    let stopped = false;
    setReleases(null);
    setLoadError("");
    const search = packSeriesId
      ? api.searchSeriesPacks(packSeriesId).then((r) => {
          if (!stopped) setGrabBookId(r.grabBookId);
          return r;
        })
      : api.searchReleasesForBook(bookId ?? 0, mediaType);
    search
      .then((r) => {
        if (stopped) return;
        setReleases(r.releases);
        setErrors(r.errors);
      })
      .catch((err: unknown) => {
        if (!stopped) setLoadError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      stopped = true;
    };
  }, [bookId, mediaType, packSeriesId]);

  const approved = useMemo(() => (releases ?? []).filter((c) => c.approved), [releases]);

  const shown = useMemo(() => {
    let list = showRejected ? (releases ?? []) : approved;
    if (proto !== "all") list = list.filter((c) => c.protocol === proto);
    const sorted = [...list];
    switch (sort) {
      case "size":
        sorted.sort((a, b) => b.size - a.size);
        break;
      case "seeders":
        sorted.sort((a, b) => b.seeders - a.seeders);
        break;
      case "age":
        sorted.sort((a, b) => ageValue(b.publishDate) - ageValue(a.publishDate));
        break;
      default:
        // Best first: approved above rejected, then by score.
        sorted.sort((a, b) =>
          a.approved !== b.approved ? (a.approved ? -1 : 1) : b.score - a.score,
        );
    }
    return sorted;
  }, [releases, approved, showRejected, proto, sort]);

  const grab = (c: ReleaseCandidate) => {
    const key = c.guid + c.indexer;
    setGrabState((s) => ({ ...s, [key]: "sending" }));
    api
      .grabRelease(c.title, c.downloadUrl, c.protocol, packSeriesId ? grabBookId : (bookId ?? 0), mediaType, c.guid)
      .then((r) => {
        setGrabState((s) => ({ ...s, [key]: `✓ sent to ${r.client}` }));
        onGrabbed?.();
      })
      .catch((err: unknown) =>
        setGrabState((s) => ({
          ...s,
          [key]: `✗ ${err instanceof Error ? err.message : String(err)}`,
        })),
      );
  };

  if (loadError) {
    return (
      <div className="release-browser">
        <p className="notice bad">Search failed: {loadError}</p>
      </div>
    );
  }
  if (releases === null) {
    return (
      <div className="release-browser">
        <p className="muted rb-searching">Searching your indexers…</p>
      </div>
    );
  }

  // Only offered as filters when actually present, and only shown at all once
  // there's a real choice between 2+ protocols among the results.
  const presentProtocols = (["usenet", "torrent", "direct"] as const).filter((p) =>
    releases.some((c) => c.protocol === p),
  );
  const torrents = presentProtocols.includes("torrent");

  return (
    <div className="release-browser">
      <div className="rb-head">
        <span className="rb-summary">
          <strong>{approved.length}</strong> approved · {releases.length} found
        </span>
        <span className="rb-controls">
          <button
            className={!showRejected ? "toggle on" : "toggle"}
            onClick={() => setShowRejected(false)}
            title="Only releases that pass your quality profile"
          >
            approved
          </button>
          <button
            className={showRejected ? "toggle on" : "toggle"}
            onClick={() => setShowRejected(true)}
            title="Everything found, with rejection reasons"
          >
            all
          </button>
          {presentProtocols.length > 1 && (
            <>
              <span className="rb-sep" />
              <button
                className={proto === "all" ? "toggle on" : "toggle"}
                onClick={() => setProto("all")}
              >
                any
              </button>
              {presentProtocols.map((p) => (
                <button
                  key={p}
                  className={proto === p ? "toggle on" : "toggle"}
                  onClick={() => setProto(p)}
                >
                  {protoIcon(p)} {p}
                </button>
              ))}
            </>
          )}
          <select value={sort} onChange={(e) => setSort(e.target.value as SortKey)} title="Sort by">
            <option value="score">Best score</option>
            <option value="size">Largest</option>
            {torrents && <option value="seeders">Most seeders</option>}
            <option value="age">Newest</option>
          </select>
          {onClose && (
            <button
              className="toggle"
              onClick={onClose}
              title="Close the release list"
              aria-label="Close the release list"
            >
              ✕
            </button>
          )}
        </span>
      </div>

      {errors.length > 0 && (
        <p className="notice bad rb-errors">Some indexers failed: {errors.join("; ")}</p>
      )}

      {shown.length === 0 ? (
        <p className="muted">
          {releases.length === 0
            ? packSeriesId
              ? "No pack releases found on your indexers."
              : "No releases found on your indexers."
            : showRejected
              ? "Nothing matches this filter."
              : "Nothing approved — switch to “all” to see what was rejected and why."}
        </p>
      ) : (
        <ul className="rows rb-list">
          {shown.map((c) => {
            const key = c.guid + c.indexer;
            const state = grabState[key];
            return (
              <li key={key} className={c.approved ? undefined : "rb-rejected"}>
                <div className="row">
                  <span className="rb-title" title={c.title}>
                    <span title={c.protocol}>{protoIcon(c.protocol)}</span> {c.title}
                  </span>
                  <span className="row-actions">
                    <span
                      className={`pill rb-score${sort === "score" ? " on" : ""}`}
                      title="Release score — higher grabs first"
                    >
                      {c.score}
                    </span>
                    {c.infoUrl && (
                      <button
                        className="toggle"
                        onClick={() => window.open(c.infoUrl, "_blank", "noopener,noreferrer")}
                        title="Open this release's source page in a new tab"
                      >
                        Source
                      </button>
                    )}
                    {state ? (
                      <span className={state.startsWith("✗") ? "notice bad" : "notice ok"}>
                        {state === "sending" ? "Sending…" : state}
                      </span>
                    ) : c.approved ? (
                      <button onClick={() => grab(c)}>Grab</button>
                    ) : (
                      <button
                        className="toggle"
                        onClick={() => grab(c)}
                        title="Rejected by your quality profile — grab it anyway"
                      >
                        grab anyway
                      </button>
                    )}
                  </span>
                </div>
                <div className="rb-meta muted">
                  {c.indexer}
                  <span className={`metric${sort === "size" ? " on" : ""}`} title="Size">
                    📦 {formatBytes(c.size) || "—"}
                  </span>
                  {c.protocol === "torrent" && (
                    <>
                      <span
                        className={`metric${sort === "seeders" ? " on" : ""}`}
                        title="Seeders"
                      >
                        ↑ {c.seeders >= 0 ? c.seeders : "N/A"}
                      </span>
                      <span className="metric" title="Leechers">
                        ↓ {leechers(c) ?? "N/A"}
                      </span>
                    </>
                  )}
                  <span className={`metric${sort === "age" ? " on" : ""}`} title="Age (published)">
                    🕓 {fmtAge(c.publishDate) || "—"}
                  </span>
                  {(c.parsed.formats ?? []).map((f) => (
                    <span key={f} className="pill rb-format">
                      {f}
                    </span>
                  ))}
                  {c.parsed.retail && <span className="pill rb-retail">retail</span>}
                  {isPack(c) && (
                    <span
                      className="pill rb-pack"
                      title={
                        c.parsed.volumeEnd
                          ? `Bundles multiple volumes (${c.parsed.volume}–${c.parsed.volumeEnd})`
                          : "Declares itself a complete run/collection — likely bundles multiple books"
                      }
                    >
                      pack
                    </span>
                  )}
                  {!c.approved && c.rejections && (
                    <span className="rb-why"> — {c.rejections.join(", ")}</span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

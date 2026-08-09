import { useCallback, useEffect, useState } from "react";
import {
  api,
  getApiKey,
  proxiedImage,
  type Book,
  type RenameMove,
  type Series,
} from "../api";
import { libraryLabels } from "../App";
import RemovePanel from "../components/RemovePanel";
import ReleaseBrowser from "../components/ReleaseBrowser";
import { downloadPct, useQueue } from "../useQueue";
import { DetailSkeleton } from "../components/Skeleton";
import { formatBytes } from "../format";
import { useUi } from "../ui";

// Full-page series detail, *arr-style: header with cover, description and
// series-level actions, then volumes/issues as rows. Manga volumes and comic
// issues expand to show file locations (manga: owned variants too) and
// per-item monitor/remove controls; a Missing section lists items not in the
// library (neither monitored nor owned), each with a one-click Monitor —
// mirroring the per-author Missing view. Magazines stay flat (issues
// materialize from grabs and scans).
export default function SeriesDetailView({
  id,
  mediaType,
  onError,
  onBack,
}: {
  id: number;
  mediaType: string;
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const label = libraryLabels[mediaType] ?? mediaType;
  const [series, setSeries] = useState<Series | null>(null);
  const [busy, setBusy] = useState(false);
  const [showPacks, setShowPacks] = useState(false);
  const [missingSelected, setMissingSelected] = useState<Set<number>>(new Set());
  const [busyMissing, setBusyMissing] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [notice, setNotice] = useState("");
  const [providerOptions, setProviderOptions] = useState<string[]>([]);
  // One shared queue poll for the whole page: every volume row shows its live
  // download state from this single (server-cached) request.
  const queue = useQueue();

  const reload = useCallback(() => {
    api
      .getSeries(id)
      .then(setSeries)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [id, onError]);

  useEffect(reload, [reload]);

  // The provider-override selector lists the providers serving this media type.
  useEffect(() => {
    api
      .getMetadataSettings()
      .then((s) =>
        setProviderOptions(
          mediaType === "manga" ? s.mangaProviders : mediaType === "comic" ? s.comicProviders : [],
        ),
      )
      .catch(() => setProviderOptions([]));
  }, [mediaType]);

  if (!series) return <DetailSkeleton />;

  const volumes = series.volumes ?? [];
  const owned = volumes.filter((v) => v.hasFile).length;
  const unitName = mediaType === "manga" ? "volume" : "issue";
  // Manga and comics split into the library (monitored or owned) and Missing
  // (neither), like books/authors, with expandable per-item rows; magazines
  // show every issue in one flat list.
  const expandable = mediaType === "manga" || mediaType === "comic";
  const inLibrary = expandable ? volumes.filter((v) => v.monitored || v.hasFile) : volumes;
  const missing = expandable ? volumes.filter((v) => !v.monitored && !v.hasFile) : [];

  const run = (action: () => Promise<unknown>) => {
    setBusy(true);
    action()
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const remove = (deleteFiles: boolean) => {
    setBusy(true);
    api
      .deleteSeries(series.id, deleteFiles)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  // Bulk monitor from Missing: a checked subset, or the whole section.
  const monitorMissing = (volumes: Book[]) => {
    setBusyMissing(true);
    Promise.allSettled(volumes.map((v) => api.monitorBook(v.id, true)))
      .then((results) => {
        const failed = results.filter((r) => r.status === "rejected").length;
        if (failed > 0) onError(`${failed} of ${volumes.length} could not be monitored`);
        setMissingSelected(new Set());
        reload();
      })
      .finally(() => setBusyMissing(false));
  };

  // Series-scoped header actions — like the author page, but only this
  // series' volumes are touched.
  const searchWanted = () => {
    setBusy(true);
    setNotice("");
    api
      .searchSeriesWanted(series.id)
      .then((r) =>
        setNotice(
          r.searched === 0
            ? "Nothing to search — every monitored volume is owned (or pending)."
            : `Searched ${r.searched} wanted volume(s), grabbed ${r.grabbed}.`,
        ),
      )
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  const scan = () => {
    setBusy(true);
    setNotice("");
    api
      .scan(mediaType)
      .then((r) => {
        setNotice(
          r.roots === 0
            ? `No ${label} root folders to scan — add one under Settings.`
            : `Scanned ${r.scanned} file(s): ${r.matched} matched, ${r.unmatched} unmatched.`,
        );
        reload();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  const previewRenames = async () => {
    setBusy(true);
    setNotice("");
    try {
      // Scan this library first (scoped — never every library) so the plan
      // reflects what's actually on disk.
      await api.scan(mediaType);
      const r = await api.renamePreview(undefined, series.id);
      setRenamePlan(r.moves);
      if (r.moves.length === 0) setNotice("This series' files already match the naming templates.");
    } catch (err) {
      setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  };

  const applyRenames = () => {
    setBusy(true);
    api
      .renameApply(undefined, series.id)
      .then((r) => {
        setNotice(`Moved ${r.moves.length} file(s)${r.skips.length ? `, ${r.skips.length} skipped` : ""}.`);
        setRenamePlan(null);
        reload();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← {label}
      </button>

      <section className="card detail-head">
        {series.coverUrl ? (
          <img className="detail-art" src={proxiedImage(series.coverUrl)} alt={`Cover of ${series.title}`} />
        ) : (
          <div className="detail-art fallback">{series.title.charAt(0)}</div>
        )}
        <div className="detail-info">
          <h2>{series.title}</h2>
          <p className="muted">
            {volumes.length} {unitName}
            {volumes.length === 1 ? "" : "s"} · {owned} owned
          </p>
          {series.description && <p className="detail-desc">{series.description}</p>}
          <div className="settings-actions">
            <button
              className={series.monitored ? "toggle on" : "toggle"}
              disabled={busy}
              title="Whether new and missing items are searched for automatically"
              onClick={() =>
                run(() => api.monitorSeries(series.id, !series.monitored, !series.monitored))
              }
            >
              {series.monitored ? "monitored" : "unmonitored"}
            </button>
            {mediaType !== "magazine" && (
              <button
                disabled={busy}
                title={`Search indexers for this series' wanted ${unitName}s`}
                onClick={searchWanted}
              >
                Search wanted
              </button>
            )}
            {mediaType !== "magazine" && (
              <button
                disabled={busy}
                title="Search for whole-series packs (v01-vNN, Complete) — importing one fills every matching volume"
                onClick={() => setShowPacks(!showPacks)}
              >
                {showPacks ? "Close packs" : "🎁 Search packs"}
              </button>
            )}
            <button disabled={busy} title="Preview naming-template moves for this series' files" onClick={previewRenames}>
              Organize…
            </button>
            <button disabled={busy} title="Scan root folders for new files" onClick={scan}>
              Scan files
            </button>
            {series.metadataSource !== "manual" && (
              <button
                disabled={busy}
                title="Re-fetch from the metadata provider; new volumes follow the monitor-new setting"
                onClick={() => run(() => api.refreshSeries(series.id))}
              >
                Refresh metadata
              </button>
            )}
            {series.metadataSource !== "manual" && providerOptions.length > 0 && (
              <select
                disabled={busy}
                title="Metadata provider override for this series — beats Settings → Metadata (including None) on the next refresh"
                value={series.providerOverride}
                onChange={(e) => run(() => api.setSeriesProvider(series.id, e.target.value))}
              >
                <option value="">Provider: follow settings</option>
                {providerOptions.map((name) => (
                  <option key={name} value={name}>
                    Provider: {name[0].toUpperCase() + name.slice(1)}
                  </option>
                ))}
              </select>
            )}
            <button
              className="danger"
              disabled={busy}
              onClick={() => setConfirmRemove(!confirmRemove)}
            >
              Remove series
            </button>
          </div>
          {confirmRemove && (
            <RemovePanel
              message={`Remove ${series.title} and all its ${unitName}s from the library?`}
              checkboxLabel="Also delete its files from disk (otherwise the next scan re-finds them as unmatched)"
              busy={busy}
              onConfirm={remove}
              onCancel={() => setConfirmRemove(false)}
            />
          )}
          {showPacks && (
            <ReleaseBrowser
              packSeriesId={series.id}
              mediaType={mediaType}
              onGrabbed={reload}
              onClose={() => setShowPacks(false)}
            />
          )}
          {renamePlan && renamePlan.length > 0 && (
            <div className="rename-plan">
              <p>{renamePlan.length} file(s) would move to match the naming templates:</p>
              <ul className="rows">
                {renamePlan.map((m) => (
                  <li key={m.fileId}>
                    <div className="move">
                      <span className="file-path muted">{m.from}</span>
                      <span className="file-path">→ {m.to}</span>
                    </div>
                  </li>
                ))}
              </ul>
              <div className="settings-actions">
                <button disabled={busy} onClick={applyRenames}>Apply</button>
                <button className="toggle" onClick={() => setRenamePlan(null)}>Cancel</button>
              </div>
            </div>
          )}
        </div>
      </section>

      <section className="card">
        <h2>
          {mediaType === "manga" ? "Volumes" : "Issues"} ({inLibrary.length})
        </h2>
        {notice && <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>}
        {inLibrary.length === 0 ? (
          <p className="muted">
            {expandable
              ? `Nothing in the library — monitor ${unitName}s from Missing below.`
              : "No issues yet — they appear when grabbed or scanned."}
          </p>
        ) : (
          <ul className="rows">
            {inLibrary.map((v) =>
              expandable ? (
                <VolumeRow
                  key={v.id}
                  volume={v}
                  mediaType={mediaType}
                  queue={queue}
                  onChanged={reload}
                  onError={onError}
                />
              ) : (
                <li key={v.id}>
                  <div className="row">
                    <span>{v.title}</span>
                    <span className="row-actions">
                      <span className={v.hasFile ? "owned yes" : "owned no"}>
                        {v.hasFile ? "owned" : "wanted"}
                      </span>
                    </span>
                  </div>
                </li>
              ),
            )}
          </ul>
        )}
      </section>

      {expandable && missing.length > 0 && (
        <section className="card">
          <div className="card-head">
            <h2>Missing ({missing.length})</h2>
            <span className="row-actions">
              {missingSelected.size > 0 && (
                <button
                  disabled={busyMissing}
                  title={`Monitor the ${missingSelected.size} checked ${unitName}(s)`}
                  onClick={() => monitorMissing(missing.filter((v) => missingSelected.has(v.id)))}
                >
                  {busyMissing ? "Monitoring…" : `+ Monitor selected (${missingSelected.size})`}
                </button>
              )}
              {missing.length > 1 && (
                <button
                  className="toggle"
                  disabled={busyMissing}
                  title={`Monitor all ${missing.length} missing ${unitName}s`}
                  onClick={() => monitorMissing(missing)}
                >
                  + Monitor all ({missing.length})
                </button>
              )}
            </span>
          </div>
          <p className="muted">
            {unitName === "volume" ? "Volumes" : "Issues"} in the series you're
            not tracking — neither monitored nor owned. Monitor adds one back
            to the library and searches for it — check several to monitor them
            in one go.
          </p>
          <ul className="rows">
            {missing.map((v) => (
              <MissingRow
                key={v.id}
                volume={v}
                selected={missingSelected.has(v.id)}
                onToggleSelect={() => {
                  const next = new Set(missingSelected);
                  if (next.has(v.id)) {
                    next.delete(v.id);
                  } else {
                    next.add(v.id);
                  }
                  setMissingSelected(next);
                }}
                onChanged={reload}
                onError={onError}
              />
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

const variantLabel = (variant?: string) =>
  variant === "color" ? "colorized" : variant === "mono" ? "monochrome" : "";

// VolumeCover shows the volume's cover, preferring one extracted from the
// owned comic file (first page), then the provider cover, then an initial —
// falling to the next source whenever an image fails to load.
function VolumeCover({ volume }: { volume: Book }) {
  const srcs: string[] = [];
  if (volume.hasFile) {
    srcs.push(`/api/v1/book/${volume.id}/cover?apikey=${encodeURIComponent(getApiKey())}`);
  }
  const provider = proxiedImage(volume.coverUrl);
  if (provider) srcs.push(provider);

  const [idx, setIdx] = useState(0);
  if (idx >= srcs.length) {
    return <div className="missing-thumb fallback">{volume.title.charAt(0)}</div>;
  }
  return (
    <img
      className="missing-thumb"
      src={srcs[idx]}
      alt=""
      loading="lazy"
      onError={() => setIdx(idx + 1)}
    />
  );
}

// coverAbout renders the compact thumbnail + blurb shared by the volume and
// Missing rows (same look as the per-author Missing view).
function coverAbout(volume: Book) {
  return (
    <div className="volume-about">
      <VolumeCover volume={volume} />
      <p className="missing-about">
        {volume.description || "No description from the metadata provider."}
      </p>
    </div>
  );
}

// VolumeRow keeps the list compact for series with hundreds of volumes or
// issues: collapsed it's just the title + an owned/wanted badge. Expanding
// reveals the cover + blurb, where each file lives (and, for manga, which
// variants are owned), plus the same per-item controls an individual book
// gets — monitor, Auto grab, Search releases, and remove-from-library.
function VolumeRow({
  volume,
  mediaType,
  queue,
  onChanged,
  onError,
}: {
  volume: Book;
  mediaType: string;
  queue: ReturnType<typeof useQueue>;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const { confirmDlg } = useUi();
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<Book | null>(null);
  const [rowBusy, setRowBusy] = useState(false);
  const [searching, setSearching] = useState(false);
  const [showReleases, setShowReleases] = useState(false);
  const [grabNotice, setGrabNotice] = useState("");
  const [confirmRemove, setConfirmRemove] = useState(false);

  const toggle = () => {
    if (!open && !detail) {
      api
        .getBook(volume.id)
        .then(setDetail)
        .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
    }
    setOpen(!open);
  };

  // Refresh both the expanded file list and the series' owned/wanted badges.
  const reloadFiles = () => {
    api.getBook(volume.id).then(setDetail).catch(() => {});
    onChanged();
  };

  const organizeFiles = async () => {
    setRowBusy(true);
    try {
      const preview = await api.renamePreview(undefined, undefined, undefined, volume.id);
      if (preview.moves.length === 0) {
        setGrabNotice("✓ Already organized — files match the naming templates.");
        return;
      }
      const ok = await confirmDlg({
        title: "Organize files",
        message:
          `Move ${preview.moves.length} file(s) to match the naming templates?\n\n` +
          preview.moves.map((m) => `${m.from}\n  → ${m.to}`).join("\n"),
        confirmLabel: "Organize",
      });
      if (!ok) return;
      await api.renameApply(undefined, undefined, undefined, volume.id);
      reloadFiles();
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setRowBusy(false);
    }
  };

  const deleteFile = async (path: string, fileId: number) => {
    const ok = await confirmDlg({
      title: "Delete file",
      message: `Delete this file from disk?\n\n${path}\n\nThe volume loses this copy; without one it counts as wanted again.`,
      confirmLabel: "Delete file",
      danger: true,
    });
    if (!ok) return;
    setRowBusy(true);
    try {
      await api.dismissFile(fileId, true);
      reloadFiles();
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setRowBusy(false);
    }
  };

  const act = (action: () => Promise<unknown>) => {
    setRowBusy(true);
    action()
      .then(onChanged)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setRowBusy(false));
  };

  const autoGrab = () => {
    setSearching(true);
    setGrabNotice("");
    api
      .autoSearchBook(volume.id, mediaType)
      .then((o) => {
        if (o.grabbed) {
          setGrabNotice(`✓ Grabbed "${o.release}" → ${o.client}`);
          queue.refresh(); // show the downloading badge right away
        } else {
          setGrabNotice(`✗ ${o.message ?? "nothing grabbed"} — Search releases shows why`);
        }
        onChanged();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setSearching(false));
  };

  const files = (detail?.files ?? []).filter((f) => f.mediaType === mediaType);
  const dl = volume.hasFile ? null : queue.activeFor(volume.id, mediaType);

  return (
    <li>
      <div className="row">
        <button className="link" onClick={toggle}>
          {open ? "▾" : "▸"} {volume.title}
        </button>
        <span className="row-actions">
          {dl ? (
            <span className="owned dl" title={`${dl.status} on ${dl.client}`}>
              ⬇ {downloadPct(dl)}
            </span>
          ) : (
            <span className={volume.hasFile ? "owned yes" : "owned no"}>
              {volume.hasFile ? "owned" : "wanted"}
            </span>
          )}
        </span>
      </div>
      {open && (
        <div className="book-detail">
          {coverAbout(volume)}
          {volume.hasFile && (
            <div className="settings-actions">
              {volume.hasColorFile && (
                <span className="owned yes" title="Colorized copy owned">
                  🎨 colorized
                </span>
              )}
              {volume.hasMonoFile && (
                <span className="owned yes" title="Monochrome copy owned">
                  ◻️ monochrome
                </span>
              )}
            </div>
          )}
          {volume.hasFile &&
            (detail === null ? (
              <p className="muted">Loading files…</p>
            ) : files.length === 0 ? (
              <p className="muted">No files recorded.</p>
            ) : (
              <ul className="rows nested">
                {files.map((f) => (
                  <li key={f.id}>
                    <div className="row">
                      <span className="file-path">
                        📄 {variantLabel(f.variant) && `${variantLabel(f.variant)} · `}
                        {f.path}
                      </span>
                      <span className="row-actions">
                        <span className="muted">
                          {f.format} · {formatBytes(f.size)}
                        </span>
                        <button
                          className="toggle"
                          disabled={rowBusy}
                          title="Move this volume's files to match the naming templates"
                          onClick={organizeFiles}
                        >
                          organize
                        </button>
                        <button
                          className="danger"
                          disabled={rowBusy}
                          title="Delete this file from disk and forget it"
                          onClick={() => deleteFile(f.path, f.id)}
                        >
                          delete
                        </button>
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            ))}
          <div className="settings-actions">
            <button
              className={volume.monitored ? "toggle on" : "toggle"}
              disabled={rowBusy}
              title="Whether this volume is searched for automatically"
              onClick={() => act(() => api.monitorBook(volume.id, !volume.monitored))}
            >
              {volume.monitored ? "monitored" : "unmonitored"}
            </button>
            <button disabled={searching} onClick={autoGrab} title="Search indexers and grab the best release">
              {searching ? "Searching…" : "Auto grab"}
            </button>
            <button
              className={showReleases ? "toggle on" : ""}
              onClick={() => setShowReleases(!showReleases)}
              title="Browse every release candidate — sort, filter, pick one yourself"
            >
              {showReleases ? "Hide releases" : "Search releases"}
            </button>
            <button
              className="danger"
              disabled={rowBusy}
              title="Remove this volume from the library"
              onClick={() => setConfirmRemove(!confirmRemove)}
            >
              Remove from library
            </button>
            {grabNotice && (
              <span className={grabNotice.startsWith("✗") ? "notice bad" : "notice ok"}>{grabNotice}</span>
            )}
          </div>
          {showReleases && (
            <ReleaseBrowser
              bookId={volume.id}
              mediaType={mediaType}
              onGrabbed={queue.refresh}
              onClose={() => setShowReleases(false)}
            />
          )}
          {confirmRemove && (
            <RemovePanel
              message={`Remove "${volume.title}" from the library? It moves to Missing, where you can add it back.`}
              checkboxLabel="Also delete its files from disk (otherwise the next scan re-finds them)"
              busy={rowBusy}
              onConfirm={(deleteFiles) =>
                act(() => api.setBookLibrary(volume.id, mediaType, false, false, deleteFiles))
              }
              onCancel={() => setConfirmRemove(false)}
            />
          )}
        </div>
      )}
    </li>
  );
}

// MissingRow is a series bibliography gap: a volume/issue neither monitored
// nor owned. Compact by default (title + one-click Monitor); expands to the
// cover and blurb, mirroring the per-author Missing view.
function MissingRow({
  volume,
  selected,
  onToggleSelect,
  onChanged,
  onError,
}: {
  volume: Book;
  selected: boolean;
  onToggleSelect: () => void;
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const monitor = () => {
    setBusy(true);
    api
      .monitorBook(volume.id, true)
      .then(onChanged)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  return (
    <li>
      <div className="row">
        <span className="row-select">
          <input
            type="checkbox"
            aria-label={`Select ${volume.title}`}
            checked={selected}
            onChange={onToggleSelect}
          />
          <button className="link" onClick={() => setOpen(!open)}>
            {open ? "▾" : "▸"} {volume.title}
          </button>
        </span>
        <span className="row-actions">
          <button disabled={busy} title="Add to the library and search for it" onClick={monitor}>
            {busy ? "Adding…" : "+ Monitor"}
          </button>
        </span>
      </div>
      {open && <div className="book-detail">{coverAbout(volume)}</div>}
    </li>
  );
}

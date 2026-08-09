import { useCallback, useEffect, useState } from "react";
import {
  api,
  type Book,
  type SearchAuthor,
  type Series,
  type SeriesResult,
  type UnmatchedOption,
} from "../api";
import { formatBytes } from "../format";
import { useUi } from "../ui";

// UnmatchedCard is the existing-file import flow, shared by every library:
// files a scan found but couldn't confidently place, each with the library's
// best suggestion (and its confidence), duplicate resolution, one-click
// import, an import-all for the confident ones, and a way to add the missing
// author/series/magazine when the file's owner isn't in the library yet.
// Prose libraries match against the author's bibliography; manga/comics
// against the series' volumes (variant-aware); magazines materialize the
// parsed issue on import.
export default function UnmatchedCard({
  mediaType,
  books,
  seriesList,
  onChanged,
  onError,
}: {
  mediaType: string;
  // Prose only: every library book, the last-resort manual match list.
  books?: Book[];
  // Series libraries only: this library's series, the manual series→volume
  // fallback when no series was auto-matched.
  seriesList?: Series[];
  onChanged: () => void;
  onError: (message: string) => void;
}) {
  const [options, setOptions] = useState<UnmatchedOption[]>([]);
  const [importingAll, setImportingAll] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .unmatchedOptions(mediaType)
      .then(setOptions)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [mediaType, onError]);

  useEffect(reload, [reload]);

  const done = () => {
    reload();
    onChanged();
  };

  const importAllMatched = () => {
    setImportingAll(true);
    setNotice("");
    api
      .importMatched(mediaType)
      .then((r) => {
        setNotice(
          `✓ Imported ${r.imported}` +
            (r.needsReview > 0 ? ` · ${r.needsReview} left for review` : ""),
        );
        done();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setImportingAll(false));
  };

  if (options.length === 0) return null;

  const confidentCount = options.filter((o) => o.confident).length;
  const kindHint =
    mediaType === "manga" || mediaType === "comic"
      ? "pick from the series' volumes"
      : mediaType === "magazine"
        ? "add the magazine so its issues can match"
        : "pick from the author's books";

  return (
    <section className="card unmatched-files">
      <div className="card-head">
        <h2>Unmatched files ({options.length})</h2>
        {confidentCount > 0 && (
          <button
            disabled={importingAll}
            onClick={importAllMatched}
            title="Import every file with a confident match"
          >
            {importingAll ? "Importing…" : `Import all matched (${confidentCount})`}
          </button>
        )}
      </div>
      <p className="muted">
        Found on disk but not matched in this library. Rows with a confident
        match import in one click; for the rest, {kindHint}. Dismiss forgets
        the record — disk is never touched.
      </p>
      {notice && <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>}
      <ul className="rows">
        {options.map((o) => (
          <UnmatchedRow
            key={o.file.id}
            mediaType={mediaType}
            option={o}
            books={books ?? []}
            seriesList={seriesList ?? []}
            onDone={done}
            onError={onError}
          />
        ))}
      </ul>
    </section>
  );
}

function UnmatchedRow({
  mediaType,
  option,
  books,
  seriesList,
  onDone,
  onError,
}: {
  mediaType: string;
  option: UnmatchedOption;
  books: Book[];
  seriesList: Series[];
  onDone: () => void;
  onError: (message: string) => void;
}) {
  const { confirmDlg } = useUi();
  const { file, candidates, duplicate } = option;
  const isVolume = mediaType === "manga" || mediaType === "comic";
  const isMagazine = mediaType === "magazine";
  const suggested = candidates.find((c) => c.id === option.suggested);
  const [bookID, setBookID] = useState(option.suggested ?? 0);
  const [busy, setBusy] = useState(false);
  const [finding, setFinding] = useState(false);
  const [authorResults, setAuthorResults] = useState<SearchAuthor[] | null>(null);
  const [seriesResults, setSeriesResults] = useState<SeriesResult[] | null>(null);
  // Manual series→volume fallback (series libraries): pick any series in the
  // library, then one of its volumes.
  const [manualSeriesID, setManualSeriesID] = useState(0);
  const [manualVolumes, setManualVolumes] = useState<Book[] | null>(null);
  const [manualVolumeID, setManualVolumeID] = useState(0);

  const pickManualSeries = (id: number) => {
    setManualSeriesID(id);
    setManualVolumes(null);
    setManualVolumeID(0);
    if (id === 0) return;
    api
      .getSeries(id)
      .then((s) => setManualVolumes(s.volumes ?? []))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  const run = (action: () => Promise<unknown>) => {
    setBusy(true);
    action()
      .then(onDone)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  // The confident import: an existing book/volume, or a magazine issue that
  // is materialized on the spot.
  const importSuggested = () => {
    if (option.suggested) {
      run(() => api.matchFile(file.id, option.suggested!));
    } else if (isMagazine && option.seriesId && option.issue) {
      run(() => api.materializeIssue(file.id, option.seriesId!, option.issue!));
    }
  };

  // The file's author/series/magazine isn't in the library: offer to add it.
  const findOwner = () => {
    if (authorResults || seriesResults) {
      setAuthorResults(null);
      setSeriesResults(null);
      return;
    }
    if (isMagazine) {
      // Magazines are added by name — no provider search needed.
      run(() => api.addMagazine(option.seriesName ?? ""));
      return;
    }
    setFinding(true);
    if (isVolume) {
      api
        .searchSeries(option.seriesName ?? "", mediaType)
        .then((results) => setSeriesResults(results.slice(0, 5)))
        .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
        .finally(() => setFinding(false));
    } else {
      api
        .searchAuthors(option.authorName ?? "")
        .then((results) => setAuthorResults(results.slice(0, 5)))
        .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
        .finally(() => setFinding(false));
    }
  };


  // Duplicate of an owned book/volume/issue: show both files and resolve.
  if (duplicate) {
    return (
      <li>
        <div className="row">
          <span className="file-path">
            ⚠️ Duplicate of <strong>{duplicate.title}</strong>
            {duplicate.year && ` (${duplicate.year})`}
            <span className="pill match-confidence" title="Match confidence">
              {duplicate.confidence}%
            </span>
          </span>
        </div>
        <ul className="rows nested">
          <li>
            <div className="row">
              <span className="file-path muted">in library: 📄 {duplicate.file.path}</span>
              <span className="muted">
                {duplicate.file.format} · {formatBytes(duplicate.file.size)}
              </span>
            </div>
          </li>
          <li>
            <div className="row">
              <span className="file-path">this file: 📄 {file.path}</span>
              <span className="row-actions">
                <span className="muted">
                  {file.format} · {formatBytes(file.size)}
                </span>
                <button
                  disabled={busy}
                  title="Use this file instead — the library's current copy is deleted from disk"
                  onClick={async () => {
                    if (
                      await confirmDlg({
                        title: "Replace the library's copy",
                        message: `Replace the library's copy of "${duplicate.title}" with this file?\n\nThe current file is deleted from disk.`,
                        confirmLabel: "Replace",
                        danger: true,
                      })
                    ) {
                      run(() => api.replaceFile(file.id, duplicate.bookId));
                    }
                  }}
                >
                  {busy ? "Working…" : "Replace"}
                </button>
                <button
                  className="danger"
                  disabled={busy}
                  title="Keep the library's copy — delete this file from disk"
                  onClick={async () => {
                    if (
                      await confirmDlg({
                        title: "Delete this file",
                        message: "Delete this file from disk? The library's copy is kept.",
                        confirmLabel: "Delete from disk",
                        danger: true,
                      })
                    ) {
                      run(() => api.dismissFile(file.id, true));
                    }
                  }}
                >
                  Delete
                </button>
                <button
                  className="toggle"
                  disabled={busy}
                  title="Forget this file without touching disk (the next scan re-finds it)"
                  onClick={() => run(() => api.dismissFile(file.id))}
                >
                  dismiss
                </button>
              </span>
            </div>
          </li>
        </ul>
      </li>
    );
  }

  // What the confident suggestion reads as.
  const suggestionLabel = isMagazine
    ? option.seriesName && option.issue
      ? `${option.seriesName} — ${option.issue}`
      : ""
    : suggested
      ? `${suggested.title}${suggested.year ? ` (${suggested.year})` : ""}`
      : "";
  const ownerName = isVolume || isMagazine ? option.seriesName : option.authorName;
  const ownerKnown = isVolume || isMagazine ? !!option.seriesId : !!option.authorId;

  return (
    <li>
      <div className="row">
        <span className="file-path">
          {file.path}
          {option.confident && suggestionLabel && (
            <span className="notice ok">
              {" "}→ {suggestionLabel}
              <span
                className="pill match-confidence"
                title="How sure the match is — an exact title is 100%; longer, more distinctive matches score higher"
              >
                {option.confidence}%
              </span>
            </span>
          )}
        </span>
        <span className="row-actions">
          {option.confident && suggestionLabel ? (
            <button
              disabled={busy}
              title={`Import as "${suggestionLabel}"`}
              onClick={importSuggested}
            >
              {busy ? "Importing…" : "Import"}
            </button>
          ) : (
            <>
              {ownerName && !ownerKnown && (
                <button
                  className="toggle"
                  disabled={busy || finding}
                  title={
                    isMagazine
                      ? `"${ownerName}" isn't in the library — add it as a magazine`
                      : `"${ownerName}" isn't in the library — search the provider and add it`
                  }
                  onClick={findOwner}
                >
                  {finding ? "Searching…" : `+ Add ${ownerName}`}
                </button>
              )}
              {isMagazine && ownerKnown && !option.issue && (
                <span className="muted" title="Rename the file with a date (2026-05-01) or issue number so it can match">
                  no issue date/number in the filename
                </span>
              )}
              {candidates.length > 0 && (
                <>
                  <select value={bookID} onChange={(e) => setBookID(Number(e.target.value))}>
                    <option value={0}>
                      {isVolume
                        ? `${option.seriesName ?? "Series"}' volumes…`
                        : option.authorName
                          ? `${option.authorName}'s books…`
                          : "Choose book…"}
                    </option>
                    {candidates.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.title}
                        {c.year ? ` (${c.year})` : ""}
                      </option>
                    ))}
                  </select>
                  <button disabled={busy || bookID === 0} onClick={() => run(() => api.matchFile(file.id, bookID))}>
                    Import
                  </button>
                </>
              )}
              {candidates.length === 0 && !isVolume && !isMagazine && books.length > 0 && (
                <>
                  <select value={bookID} onChange={(e) => setBookID(Number(e.target.value))}>
                    <option value={0}>Match to book…</option>
                    {books.map((b) => (
                      <option key={b.id} value={b.id}>{b.title}</option>
                    ))}
                  </select>
                  <button disabled={busy || bookID === 0} onClick={() => run(() => api.matchFile(file.id, bookID))}>
                    Import
                  </button>
                </>
              )}
              {isVolume && candidates.length === 0 && seriesList.length > 0 && (
                <>
                  <select
                    value={manualSeriesID}
                    title="Match this file to any series in the library"
                    onChange={(e) => pickManualSeries(Number(e.target.value))}
                  >
                    <option value={0}>Match to series…</option>
                    {seriesList.map((sr) => (
                      <option key={sr.id} value={sr.id}>
                        {sr.title}
                      </option>
                    ))}
                  </select>
                  {manualSeriesID > 0 && manualVolumes === null && (
                    <span className="muted">loading volumes…</span>
                  )}
                  {manualVolumes !== null && manualVolumes.length === 0 && (
                    <span className="muted">this series has no volumes yet</span>
                  )}
                  {manualVolumes !== null && manualVolumes.length > 0 && (
                    <>
                      <select
                        value={manualVolumeID}
                        onChange={(e) => setManualVolumeID(Number(e.target.value))}
                      >
                        <option value={0}>Volume…</option>
                        {manualVolumes.map((v) => (
                          <option key={v.id} value={v.id}>
                            {v.title}
                            {v.hasFile ? " (owned)" : ""}
                          </option>
                        ))}
                      </select>
                      <button
                        disabled={busy || manualVolumeID === 0}
                        onClick={() => run(() => api.matchFile(file.id, manualVolumeID))}
                      >
                        Import
                      </button>
                    </>
                  )}
                </>
              )}
            </>
          )}
          <button className="toggle" disabled={busy} onClick={() => run(() => api.dismissFile(file.id))}>
            dismiss
          </button>
        </span>
      </div>
      {authorResults && (
        <ul className="rows nested">
          {authorResults.length === 0 && (
            <li className="muted fb-empty">No author named “{option.authorName}” found on the provider.</li>
          )}
          {authorResults.map((a) => (
            <li key={a.foreignAuthorId}>
              <div className="row">
                <span>
                  {a.name}
                  {(a.bookCount ?? 0) > 0 && <span className="muted"> · {a.bookCount} books</span>}
                </span>
                <button
                  disabled={busy}
                  title={`Add ${a.name} to this library — their bibliography becomes matchable`}
                  onClick={() => run(() => api.addAuthor(a.foreignAuthorId, file.mediaType))}
                >
                  {busy ? "Adding…" : "Add author"}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {seriesResults && (
        <ul className="rows nested">
          {seriesResults.length === 0 && (
            <li className="muted fb-empty">No series named “{option.seriesName}” found on the provider.</li>
          )}
          {seriesResults.map((sr) => (
            <li key={sr.foreignSeriesId}>
              <div className="row">
                <span>
                  {sr.title}
                  {sr.issueCount > 0 && <span className="muted"> · {sr.issueCount} volumes</span>}
                  {sr.authorName && <span className="muted"> · {sr.authorName}</span>}
                </span>
                <button
                  disabled={busy}
                  title={`Add ${sr.title} — its volumes become matchable`}
                  onClick={() => run(() => api.addSeries(mediaType, sr.foreignSeriesId))}
                >
                  {busy ? "Adding…" : "Add series"}
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

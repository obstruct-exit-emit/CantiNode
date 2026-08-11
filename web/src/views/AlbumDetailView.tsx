import { useCallback, useEffect, useState } from "react";
import {
  api,
  musicAlbumCoverUrl,
  type MusicAlbum,
  type MusicTrack,
  type MusicTrackFile,
  type RenameMove,
} from "../api";
import RemovePanel from "../components/RemovePanel";
import { DetailSkeleton } from "../components/Skeleton";
import { formatBytes } from "../format";
import { useUi } from "../ui";

// Full-page album detail: header with cover art, release info, and
// album-scoped Scan/Organize/Remove actions (unlike the artist page's
// versions, these never touch a sibling album's files), then its tracks —
// each with the file(s) matched to it (path, format, organize/write-tags/
// delete actions), mirroring the book page's Files section.
export default function AlbumDetailView({
  id,
  onError,
  onBack,
}: {
  id: number;
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const { confirmDlg } = useUi();
  const [album, setAlbum] = useState<MusicAlbum | null>(null);
  const [tracks, setTracks] = useState<MusicTrack[]>([]);
  const [files, setFiles] = useState<Record<number, MusicTrackFile[]>>({});
  const [busyId, setBusyId] = useState<number | null>(null);
  const [headerBusy, setHeaderBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    Promise.all([api.getMusicAlbum(id), api.listMusicTracks(id)])
      .then(async ([al, tr]) => {
        setAlbum(al);
        setTracks(tr);
        const perTrack = await Promise.all(tr.map((t) => api.listMusicTrackFiles(t.id)));
        const byTrack: Record<number, MusicTrackFile[]> = {};
        tr.forEach((t, i) => {
          byTrack[t.id] = perTrack[i];
        });
        setFiles(byTrack);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [id, onError]);

  useEffect(reload, [reload]);

  if (!album) return <DetailSkeleton />;

  const scan = () => {
    setHeaderBusy(true);
    setNotice("");
    api
      .scanMusicAlbum(album.id)
      .then((r) => {
        setNotice(
          `✓ Scan complete — ${r.filesFound} file(s) found, ${r.filesMatched} matched` +
            (r.filesOrganized ? `, ${r.filesOrganized} organized` : "") +
            (r.errors && r.errors.length ? `, ${r.errors.length} error(s)` : "") +
            ".",
        );
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setHeaderBusy(false));
  };

  const previewOrganize = async () => {
    setHeaderBusy(true);
    setNotice("");
    try {
      const r = await api.previewOrganizeMusicAlbum(album.id);
      setRenamePlan(r.moves);
      if (r.moves.length === 0) setNotice("This album's files already match the naming template.");
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setHeaderBusy(false);
    }
  };

  const applyOrganize = () => {
    setHeaderBusy(true);
    api
      .organizeMusicAlbum(album.id)
      .then((r) => {
        setNotice(`✓ Moved ${r.moves.length} file(s)${r.errors.length ? `, ${r.errors.length} failed` : ""}.`);
        setRenamePlan(null);
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setHeaderBusy(false));
  };

  const removeAlbum = (deleteFiles: boolean) => {
    setHeaderBusy(true);
    api
      .removeMusicAlbum(album.id, deleteFiles)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setHeaderBusy(false));
  };

  const organizeFile = async (f: MusicTrackFile) => {
    setBusyId(f.id);
    setNotice("");
    try {
      const preview = await api.previewOrganizeTrackFile(f.id);
      const ok = await confirmDlg({
        title: "Organize file",
        message: `Move this file to match the naming template?\n\n${f.path}\n  → ${preview.path}`,
        confirmLabel: "Organize",
      });
      if (!ok) return;
      await api.organizeTrackFile(f.id);
      setNotice("✓ File organized.");
      reload();
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusyId(null);
    }
  };

  const writeTags = async (f: MusicTrackFile) => {
    setBusyId(f.id);
    setNotice("");
    try {
      await api.writeMusicTags(f.id);
      setNotice("✓ Tags written to file.");
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusyId(null);
    }
  };

  const removeFile = async (f: MusicTrackFile) => {
    const ok = await confirmDlg({
      title: "Delete file",
      message: `Delete this file from disk?\n\n${f.path}`,
      confirmLabel: "Delete file",
      danger: true,
    });
    if (!ok) return;
    setBusyId(f.id);
    setNotice("");
    try {
      await api.deleteTrackFile(f.id);
      setNotice("✓ File deleted.");
      reload();
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusyId(null);
    }
  };

  const year = album.releaseDate ? album.releaseDate.slice(0, 4) : "";

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← Artist
      </button>

      <section className="card detail-head">
        {album.mbid ? (
          <img className="detail-art" src={musicAlbumCoverUrl(album.id)} alt={`Cover of ${album.title}`} />
        ) : (
          <div className="detail-art fallback">{album.title.charAt(0)}</div>
        )}
        <div className="detail-info">
          <h2>
            {album.title}
            {year && <span className="muted"> ({year})</span>}
          </h2>
          <p className="muted">
            {album.primaryType || "Album"} · {tracks.length} track{tracks.length === 1 ? "" : "s"}
          </p>
          <div className="settings-actions">
            <button disabled={headerBusy} onClick={scan} title="Scan this album's own folder for new or changed files">
              Scan files
            </button>
            <button disabled={headerBusy} onClick={previewOrganize} title="Preview naming-template moves for this album's files only">
              Organize…
            </button>
            <button className="danger" disabled={headerBusy} onClick={() => setConfirmRemove(!confirmRemove)}>
              Remove album
            </button>
          </div>
          {notice && <p className="muted">{notice}</p>}
          {renamePlan && renamePlan.length > 0 && (
            <div className="rename-plan">
              <p>{renamePlan.length} file(s) would move to match the naming template:</p>
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
                <button disabled={headerBusy} onClick={applyOrganize}>Apply</button>
                <button className="toggle" onClick={() => setRenamePlan(null)}>Cancel</button>
              </div>
            </div>
          )}
          {confirmRemove && (
            <RemovePanel
              message={`Remove "${album.title}" from the library? The artist and its other albums are untouched.`}
              checkboxLabel="Also delete its files from disk"
              busy={headerBusy}
              onConfirm={removeAlbum}
              onCancel={() => setConfirmRemove(false)}
            />
          )}
        </div>
      </section>

      <section className="card">
        <h2>Tracks ({tracks.length})</h2>
        {tracks.length === 0 ? (
          <p className="muted">No tracks recorded for this album yet.</p>
        ) : (
          <ul className="rows">
            {tracks.map((t) => {
              const tfiles = files[t.id] ?? [];
              return (
                <li key={t.id}>
                  <div className="row">
                    <span>
                      {t.discNumber > 1 ? `${t.discNumber}.` : ""}
                      {String(t.trackNumber).padStart(2, "0")} — {t.title}
                    </span>
                    <span className={tfiles.length > 0 ? "owned yes" : "owned no"}>
                      {tfiles.length > 0 ? "owned" : "no file"}
                    </span>
                  </div>
                  {tfiles.map((f) => (
                    <div className="row nested" key={f.id}>
                      <span className="file-path muted">📄 {f.path}</span>
                      <span className="row-actions">
                        <span className="muted">
                          {f.format} · {formatBytes(f.sizeBytes)}
                        </span>
                        <button
                          className="toggle"
                          disabled={busyId !== null}
                          title="Move this file to match the naming template"
                          onClick={() => organizeFile(f)}
                        >
                          organize
                        </button>
                        <button
                          className="toggle"
                          disabled={busyId !== null}
                          title="Write this track's metadata back into the file's own tags"
                          onClick={() => writeTags(f)}
                        >
                          write tags
                        </button>
                        <button
                          className="danger"
                          disabled={busyId !== null}
                          title="Delete this file from disk and forget it"
                          onClick={() => removeFile(f)}
                        >
                          delete
                        </button>
                      </span>
                    </div>
                  ))}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </>
  );
}

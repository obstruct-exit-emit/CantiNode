import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  getApiKey,
  type MusicAlbum,
  type MusicTrack,
  type MusicTrackFile,
  type RenameMove,
} from "../api";
import AlbumCover from "../components/AlbumCover";
import RemovePanel from "../components/RemovePanel";
import ReleaseBrowser from "../components/ReleaseBrowser";
import { DetailSkeleton } from "../components/Skeleton";
import TrackCreditsModal from "../components/TrackCreditsModal";
import TrackFileTagsModal from "../components/TrackFileTagsModal";
import WriteTagsDialog from "../components/WriteTagsDialog";
import { formatBytes } from "../format";

// commonBasePath returns the deepest directory every one of paths shares —
// e.g. two discs' worth of files under .../Moonglow (2CD)/CD1/... and
// .../Moonglow (2CD)/CD2/... share .../Moonglow (2CD). Compared segment by
// segment (split on "/"), not as a raw string prefix, so it never cuts a
// path off mid-directory-name. Empty for no paths at all.
function commonBasePath(paths: string[]): string {
  if (paths.length === 0) return "";
  const dirs = paths.map((p) => p.split("/").slice(0, -1));
  let common = dirs[0];
  for (const d of dirs.slice(1)) {
    let i = 0;
    while (i < common.length && i < d.length && common[i] === d[i]) i++;
    common = common.slice(0, i);
  }
  return common.join("/");
}

// losslessFormats is a container-level classification, not true codec
// sniffing — MusicTrackFile.format (from internal/tagreader.Tags.Format)
// identifies the container/extension, not what's actually encoded inside
// it. m4a/ogg are treated as lossy since that's what they overwhelmingly
// are in practice (AAC, Vorbis) even though both containers can technically
// hold a lossless codec (ALAC, FLAC-in-Ogg) too rare to be worth a
// false-lossless label for the common case.
const losslessFormats = new Set(["flac", "wav", "dsf"]);

type Quality = "lossless" | "lossy" | "mixed";

// albumQuality summarizes every owned file's format into one label — a
// mixed result (some tracks lossless, some not) is real and worth
// surfacing as its own state, not silently rounded to either extreme.
// null when there's nothing owned yet to judge.
function albumQuality(files: MusicTrackFile[]): Quality | null {
  if (files.length === 0) return null;
  let lossless = false;
  let lossy = false;
  for (const f of files) {
    if (losslessFormats.has(f.format.toLowerCase())) lossless = true;
    else lossy = true;
  }
  if (lossless && lossy) return "mixed";
  return lossless ? "lossless" : "lossy";
}

// titleCase renders a MusicBrainz disambiguation ("limited edition
// artbook", always lowercase free text) as a proper label ("Limited
// Edition Artbook").
function titleCase(s: string): string {
  return s.replace(/\b\w/g, (c) => c.toUpperCase());
}

// TrackArtistCredit renders a track's own featured guests — internal/
// musicscanner's joinArtistCredit flattens a MusicBrainz artist-credit
// (which can run to half a dozen names on a guest-vocalist-heavy track,
// Avantasia being the extreme case) into one ", "-joined string, since
// that's all the API ever sends; there's no structured list to work with
// here, only this split. The credit's own first name is always the
// recording's primary artist (already the album's own artist whenever
// this even renders — see applyMatch's blanking rule), so it's dropped
// here rather than re-listed as one of the "featuring" names. Nothing
// shows on the row itself — just a button naming the featured count,
// opening TrackCreditsModal for the actual names. Renders nothing at all
// when there's no one left to feature (a track whose only stored credit
// is a single differing performer — e.g. a Various Artists compilation
// track — rather than an added guest).
function TrackArtistCredit({ credit, trackTitle }: { credit: string; trackTitle: string }) {
  const [showCredits, setShowCredits] = useState(false);
  const featuring = credit.split(", ").slice(1);
  if (featuring.length === 0) {
    return null;
  }
  return (
    <>
      <button
        className="toggle col-credits"
        onClick={(e) => {
          e.stopPropagation();
          setShowCredits(true);
        }}
      >
        {featuring.length} Featuring
      </button>
      {showCredits && (
        <TrackCreditsModal trackTitle={trackTitle} names={featuring} onClose={() => setShowCredits(false)} />
      )}
    </>
  );
}

// Full-page album detail: header with cover art, release info, and
// album-scoped Scan/Organize/Write tags/Remove actions (unlike the artist
// page's versions, these never touch a sibling album's files), then its
// tracks with the file(s) matched to each — path/format/size only, no
// per-file actions: organize/write-tags/delete are all bulk, album- or
// artist-scoped actions now, not something to repeat per file.
export default function AlbumDetailView({
  id,
  onError,
  onBack,
}: {
  id: number;
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const [album, setAlbum] = useState<MusicAlbum | null>(null);
  const [tracks, setTracks] = useState<MusicTrack[]>([]);
  const [files, setFiles] = useState<Record<number, MusicTrackFile[]>>({});
  const [headerBusy, setHeaderBusy] = useState(false);
  const [showUpgrade, setShowUpgrade] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [notice, setNotice] = useState("");
  const [description, setDescription] = useState("");
  const [versionLabel, setVersionLabel] = useState("");
  const [tagsFile, setTagsFile] = useState<MusicTrackFile | null>(null);
  const [showWriteTags, setShowWriteTags] = useState(false);

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

  // Lazily fetches (and, server-side, caches) the description only on an
  // album's first-ever view — descriptionFetchedAt already set means the
  // plain getMusicAlbum response above already carried it for free, no
  // extra request needed. Best-effort: a failure just leaves the
  // description blank, the same cosmetic-only treatment every other
  // TheAudioDB call in this app gets.
  useEffect(() => {
    if (!album) return;
    if (album.descriptionFetchedAt !== undefined) {
      setDescription(album.description);
      return;
    }
    api
      .getMusicAlbumDescription(album.id)
      .then((r) => setDescription(r.description))
      .catch(() => {});
  }, [album]);

  // Version (edition/pressing) label — no extra caching of its own needed
  // here: listReleaseGroupVersions already reads straight from the
  // release_group_versions table the discography sync eagerly warmed, so
  // this is already a cheap local DB read server-side, not a live
  // MusicBrainz call. Finds the one cached version matching this album's
  // own specific release (album.mbid), not just the release group.
  useEffect(() => {
    if (!album || !album.releaseGroupMbid || !album.mbid) {
      setVersionLabel("");
      return;
    }
    api
      .listReleaseGroupVersions(album.releaseGroupMbid)
      .then((versions) => {
        const v = versions.find((v) => v.releaseMbid === album.mbid);
        if (!v) {
          setVersionLabel("");
          return;
        }
        const parts = [v.mediaSummary, v.disambiguation && titleCase(v.disambiguation)].filter(Boolean);
        setVersionLabel(parts.join(" · "));
      })
      .catch(() => setVersionLabel(""));
  }, [album]);

  const allFiles = useMemo(() => Object.values(files).flat(), [files]);
  const basePath = useMemo(() => commonBasePath(allFiles.map((f) => f.path)), [allFiles]);
  const quality = useMemo(() => albumQuality(allFiles), [allFiles]);
  const totalBytes = useMemo(() => allFiles.reduce((sum, f) => sum + f.sizeBytes, 0), [allFiles]);

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

  const writeTags = (clear: boolean) => {
    setHeaderBusy(true);
    setNotice("");
    api
      .writeMusicTagsForAlbum(album.id, clear)
      .then((r) => {
        setNotice(`✓ Wrote tags to ${r.written} file(s)${r.errors.length ? `, ${r.errors.length} failed` : ""}.`);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setHeaderBusy(false));
  };

  const confirmWriteTags = (clear: boolean) => {
    setShowWriteTags(false);
    writeTags(clear);
  };

  const year = album.releaseDate ? album.releaseDate.slice(0, 4) : "";

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← Artist
      </button>

      <section className="card detail-head">
        <AlbumCover
          albumId={album.id}
          mbid={album.mbid}
          title={album.title}
          className="detail-art"
          alt={`Cover of ${album.title}`}
        />
        <div className="detail-info">
          <h2>
            {album.title}
            {year && <span className="muted"> ({year})</span>}
          </h2>
          <p className="muted">
            {album.primaryType || "Album"} · {tracks.length} track{tracks.length === 1 ? "" : "s"}
          </p>
          {(versionLabel || quality || totalBytes > 0) && (
            <div className="detail-stats">
              {versionLabel && (
                <div className="detail-stat">
                  <span className="detail-stat-label">Version</span>
                  <span className="detail-stat-value" title={versionLabel}>{versionLabel}</span>
                </div>
              )}
              {quality && (
                <div className="detail-stat">
                  <span className="detail-stat-label">Quality</span>
                  <span className={`detail-stat-value quality-${quality}`}>
                    {quality === "lossless" ? "Lossless" : quality === "mixed" ? "Mixed" : "Lossy"}
                  </span>
                </div>
              )}
              {totalBytes > 0 && (
                <div className="detail-stat">
                  <span className="detail-stat-label">Size</span>
                  <span className="detail-stat-value">{formatBytes(totalBytes)}</span>
                </div>
              )}
            </div>
          )}
          {basePath && (
            <div className="detail-stats">
              <div className="detail-stat">
                <span className="detail-stat-label">Path</span>
                <span className="detail-stat-value" title={basePath}>{basePath}</span>
              </div>
            </div>
          )}
          {description && <p className="detail-desc">{description}</p>}
          {album.releaseGroupMbid && (
            <div className="settings-actions detail-links">
              <a
                className="toggle"
                href={`https://musicbrainz.org/release-group/${album.releaseGroupMbid}`}
                target="_blank"
                rel="noreferrer"
                title="Open this album on MusicBrainz"
              >
                MusicBrainz ↗
              </a>
              <a
                className="toggle"
                href={`/api/v1/music/album/${album.id}/audiodb-link?apikey=${encodeURIComponent(getApiKey())}`}
                target="_blank"
                rel="noreferrer"
                title="Open this album on TheAudioDB (if it has one)"
              >
                TheAudioDB ↗
              </a>
            </div>
          )}
          <div className="settings-actions">
            <button disabled={headerBusy} onClick={scan} title="Scan this album's own folder for new or changed files">
              Scan files
            </button>
            <button disabled={headerBusy} onClick={previewOrganize} title="Preview naming-template moves for this album's files only">
              Organize…
            </button>
            <button
              disabled={headerBusy}
              onClick={() => setShowWriteTags(true)}
              title="Write this album's matched metadata back into every file's own tags"
            >
              Write tags…
            </button>
            <button
              className={showUpgrade ? "toggle on" : ""}
              onClick={() => setShowUpgrade(!showUpgrade)}
              title={`Search for a better release than what's owned now — requires "Allow upgrades" on the music quality profile`}
            >
              {showUpgrade ? "Hide upgrade search" : "Search upgrade"}
            </button>
            <button className="danger" disabled={headerBusy} onClick={() => setConfirmRemove(!confirmRemove)}>
              Remove album
            </button>
          </div>
          {notice && <p className="muted">{notice}</p>}
          {showUpgrade && (
            <ReleaseBrowser
              upgradeAlbumId={album.id}
              onGrabbed={reload}
              onClose={() => setShowUpgrade(false)}
            />
          )}
          {renamePlan && renamePlan.length > 0 && (
            <div className="rename-plan">
              <p>{renamePlan.length} file(s) would move to match the naming template:</p>
              <ul className="rows">
                <li key={renamePlan[0].fileId}>
                  <div className="move">
                    <span className="file-path muted">{renamePlan[0].from}</span>
                    <span className="file-path">→ {renamePlan[0].to}</span>
                  </div>
                </li>
              </ul>
              {renamePlan.length > 1 && (
                <details className="disclosure">
                  <summary>Show {renamePlan.length - 1} more</summary>
                  <div className="disclosure-body">
                    <ul className="rows">
                      {renamePlan.slice(1).map((m) => (
                        <li key={m.fileId}>
                          <div className="move">
                            <span className="file-path muted">{m.from}</span>
                            <span className="file-path">→ {m.to}</span>
                          </div>
                        </li>
                      ))}
                    </ul>
                  </div>
                </details>
              )}
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
              // The common case (one file per track) shows format/size and
              // the Tags button right on the track's own row, alongside
              // "owned" — a track with more than one file (a stray
              // duplicate import, most commonly) falls back to a nested row
              // per file below, since "owned" can't point at just one of
              // them.
              const single = tfiles.length === 1 ? tfiles[0] : null;
              return (
                <li key={t.id}>
                  <div className="row">
                    <span>
                      {t.discNumber > 1 ? `${t.discNumber}.` : ""}
                      {String(t.trackNumber).padStart(2, "0")} — {t.title}
                    </span>
                    <span className="track-row-actions">
                      {t.artistCredit && <TrackArtistCredit credit={t.artistCredit} trackTitle={t.title} />}
                      {single && (
                        <button
                          className="toggle col-tags"
                          title="Show this file's own embedded tags"
                          onClick={() => setTagsFile(single)}
                        >
                          Tags
                        </button>
                      )}
                      {single && (
                        <>
                          <span className="pill col-format">{single.format}</span>
                          <span className="pill col-size">{formatBytes(single.sizeBytes)}</span>
                        </>
                      )}
                      <span className={`col-owned ${tfiles.length > 0 ? "owned yes" : "owned no"}`}>
                        {tfiles.length > 0 ? "owned" : "no file"}
                      </span>
                    </span>
                  </div>
                  {tfiles.length > 1 &&
                    tfiles.map((f) => (
                      <div className="row nested" key={f.id}>
                        <span title={f.path}>
                          <span className="pill">{f.format}</span>
                          <span className="pill">{formatBytes(f.sizeBytes)}</span>
                        </span>
                        <button
                          className="toggle"
                          title="Show this file's own embedded tags"
                          onClick={() => setTagsFile(f)}
                        >
                          Tags
                        </button>
                      </div>
                    ))}
                </li>
              );
            })}
          </ul>
        )}
      </section>
      {tagsFile && (
        <TrackFileTagsModal
          fileId={tagsFile.id}
          fileName={tagsFile.path.split("/").pop() ?? tagsFile.path}
          onClose={() => setTagsFile(null)}
        />
      )}
      {showWriteTags && (
        <WriteTagsDialog scope="album" onConfirm={confirmWriteTags} onClose={() => setShowWriteTags(false)} />
      )}
    </>
  );
}

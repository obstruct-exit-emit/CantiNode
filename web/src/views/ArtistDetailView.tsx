import { useCallback, useEffect, useState } from "react";
import {
  api,
  getApiKey,
  musicReleaseGroupCoverUrl,
  proxiedImage,
  type ArtistMove,
  type MusicAlbum,
  type MusicArtist,
  type MusicReleaseGroup,
  type RootFolder,
  type ReleaseGroupTracklist,
  type RenameMove,
  type WantedAlbum,
} from "../api";
import AlbumCover from "../components/AlbumCover";
import RemovePanel from "../components/RemovePanel";
import ReleaseBrowser from "../components/ReleaseBrowser";
import { DetailSkeleton } from "../components/Skeleton";
import {
  SortSelect,
  DirectionButtons,
  sortReleaseGroups,
  releaseCategory,
  groupByReleaseCategory,
  defaultDirFor,
  type SortDir,
} from "../components/SortControl";
import WriteTagsDialog from "../components/WriteTagsDialog";
import { formatBytes, formatDuration } from "../format";

// Albums section display: "grid" (current default, large covers), "compact"
// (same grid, smaller covers), or "list" (a plain title + status row).
type AlbumsView = "grid" | "compact" | "list";

// GridAlbum unifies owned albums and wanted albums into one list for the
// Albums grid — mirroring how a library-member book shows up whether or not
// it's downloaded yet, rather than owned/wanted living in separate sections.
type GridAlbum =
  | { kind: "owned"; key: string; id: number; title: string; releaseDate: string; primaryType: string; mbid: string }
  | {
      kind: "wanted";
      key: string;
      id: number;
      title: string;
      releaseDate: string;
      primaryType: string;
      status: WantedAlbum["status"];
      releaseGroupMbid: string;
    };

function sortGridAlbums(items: GridAlbum[], key: string, dir: SortDir): GridAlbum[] {
  const by = [...items];
  switch (key) {
    case "title":
      by.sort((a, b) => a.title.localeCompare(b.title));
      break;
    case "date": // ascending = oldest first
      by.sort((a, b) => (a.releaseDate || "").localeCompare(b.releaseDate || ""));
      break;
    default:
      break;
  }
  return dir === "desc" ? by.reverse() : by;
}

// WantedPoster shows a wanted/missing album's cover art via its cached
// representative release (see musicReleaseGroupCoverUrl) — falls back to
// the same plain letter tile owned albums use when there's genuinely no
// cover art (a 404, most commonly a release Cover Art Archive has nothing
// for) rather than a broken-image icon.
function WantedPoster({ releaseGroupMbid, title }: { releaseGroupMbid: string; title: string }) {
  const [failed, setFailed] = useState(false);
  if (failed || !releaseGroupMbid) {
    return <div className="poster fallback">{title.charAt(0)}</div>;
  }
  return (
    <img
      className="poster"
      src={musicReleaseGroupCoverUrl(releaseGroupMbid)}
      alt=""
      loading="lazy"
      onError={() => setFailed(true)}
    />
  );
}

// Full-page artist detail, mirroring the author page: header with portrait,
// bio and artist-level actions, then one Albums grid holding both owned and
// wanted albums (badged accordingly — clicking a wanted one opens its
// release search inline), and a Missing section for discography gaps that
// aren't wanted yet.
export default function ArtistDetailView({
  id,
  isAdmin,
  onError,
  onBack,
  onOpenAlbum,
}: {
  id: number;
  isAdmin: boolean;
  onError: (message: string) => void;
  onBack: () => void;
  onOpenAlbum: (albumId: number) => void;
}) {
  const [artist, setArtist] = useState<MusicArtist | null>(null);
  const [albums, setAlbums] = useState<MusicAlbum[]>([]);
  const [wanted, setWanted] = useState<WantedAlbum[]>([]);
  const [selectedWantedId, setSelectedWantedId] = useState<number | null>(null);
  // Mirrors LibriNode's book page: opening a wanted album never searches by
  // itself — ReleaseBrowser (and the indexer search it fires on mount) only
  // shows up once the user explicitly asks for it via "Search releases".
  const [showReleases, setShowReleases] = useState(false);
  const [removingWanted, setRemovingWanted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [showWriteTags, setShowWriteTags] = useState(false);
  const [notice, setNotice] = useState("");
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [rootFolders, setRootFolders] = useState<RootFolder[]>([]);
  const [moveTargetId, setMoveTargetId] = useState<number | "">("");
  const [movePlan, setMovePlan] = useState<{ moves: ArtistMove[]; totalBytes: number } | null>(null);
  const [moving, setMoving] = useState(false);
  const [moveResult, setMoveResult] = useState<{ moved: number; errors: string[]; error?: string } | null>(null);
  const [reloadTick, setReloadTick] = useState(0);
  const [albumsSort, setAlbumsSort] = useState("date");
  const [albumsDir, setAlbumsDir] = useState<SortDir>(defaultDirFor("date"));
  const [albumsView, setAlbumsView] = useState<AlbumsView>("grid");
  const changeAlbumsSort = (key: string) => {
    setAlbumsSort(key);
    setAlbumsDir(defaultDirFor(key));
  };

  const reload = useCallback(() => {
    Promise.all([api.getMusicArtist(id), api.listMusicAlbums(id), api.listWantedMusicAlbums(id)])
      .then(([a, al, w]) => {
        setArtist(a);
        setAlbums(al);
        setWanted(w);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [id, onError]);

  useEffect(reload, [reload]);

  // Root folders load once — used only to populate the "Move to…"
  // dropdown, no need to refresh on every reload tick. GET /rootfolder is
  // admin-only (root-folder management is an admin concern everywhere
  // else in this app too — Settings' own Root Folders card is admin-
  // gated the same way), so a member account would otherwise 403 on this
  // call, every single time it visits any artist page, for a dropdown it
  // could never use anyway.
  useEffect(() => {
    if (!isAdmin) return;
    api
      .listRootFolders()
      .then(setRootFolders)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [isAdmin, onError]);

  // Polls move status while a move is running — an interval owned by
  // React (cleared on unmount or once `moving` flips back to false)
  // rather than a free-standing recursive setTimeout, so navigating away
  // from this page mid-move doesn't leave a detached poll loop calling
  // setState on an unmounted component every second until the move
  // finishes. Mirrors the useEffect+setInterval shape ActivityView and
  // App.tsx already use for their own polling.
  useEffect(() => {
    if (!moving) return;
    const timer = setInterval(() => {
      api
        .musicMoveStatus()
        .then((state) => {
          if (state.running) return;
          setMoving(false);
          setMoveTargetId("");
          setMovePlan(null);
          setMoveResult({ moved: state.moved?.length ?? 0, errors: state.errors ?? [], error: state.error });
          reload();
        })
        .catch((err: unknown) => {
          setMoving(false);
          onError(String(err instanceof Error ? err.message : err));
        });
    }, 1000);
    return () => clearInterval(timer);
  }, [moving, onError, reload]);

  // Shared by the Missing and Wanted cards below: wanting, un-wanting, or
  // monitoring an album moves it between the two lists, so either side
  // needs the other to refetch too — bumping reloadTick (both cards' own
  // effects depend on it) does that; reload() also picks up a changed
  // monitored badge on the artist header.
  const refreshMissingAndWanted = () => {
    reload();
    setReloadTick((t) => t + 1);
  };

  if (!artist) return <DetailSkeleton />;

  const headerAction = (action: () => Promise<string>) => {
    setBusy(true);
    setNotice("");
    action()
      .then((msg) => {
        setNotice(msg);
        reload();
        setReloadTick((t) => t + 1);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const toggleMonitor = () =>
    headerAction(async () => {
      if (artist.isMonitored) {
        await api.unmonitorMusicArtist(artist.id);
        return "✓ Unmonitored";
      }
      await api.refreshMusicArtist(artist.id);
      return "✓ Monitored — discography refreshed";
    });

  const refresh = () =>
    headerAction(async () => {
      await api.refreshMusicArtist(artist.id);
      return "✓ Metadata refreshed";
    });

  const scan = () =>
    headerAction(async () => {
      await api.triggerMusicScan();
      return "Scan started — this can take a while for a large library.";
    });

  const previewOrganize = async () => {
    setBusy(true);
    setNotice("");
    try {
      const r = await api.previewOrganizeMusicArtist(artist.id);
      setRenamePlan(r.moves);
      if (r.moves.length === 0) setNotice("This artist's files already match the naming template.");
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setBusy(false);
    }
  };

  const applyOrganize = () => {
    setBusy(true);
    api
      .organizeMusicArtist(artist.id)
      .then((r) => {
        setNotice(`Moved ${r.moves.length} file(s)${r.errors.length ? `, ${r.errors.length} failed` : ""}.`);
        setRenamePlan(null);
        reload();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const writeTags = (clear: boolean) => {
    setBusy(true);
    setNotice("");
    api
      .writeMusicTagsForArtist(artist.id, clear)
      .then((r) => {
        setNotice(`Wrote tags to ${r.written} file(s)${r.errors.length ? `, ${r.errors.length} failed` : ""}.`);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const confirmWriteTags = (clear: boolean) => {
    setShowWriteTags(false);
    writeTags(clear);
  };

  // previewMove loads what a move to the just-picked root folder would do
  // (which files, total size) — the "warning" step: nothing moves until
  // the user reviews this and explicitly approves via applyMove.
  const previewMove = (rootFolderId: number | "") => {
    setMoveTargetId(rootFolderId);
    setMovePlan(null);
    setMoveResult(null);
    if (rootFolderId === "") return;
    setBusy(true);
    setNotice("");
    api
      .previewMoveMusicArtist(artist.id, rootFolderId)
      .then((r) => {
        setMovePlan(r);
        if (r.moves.length === 0) setNotice("This artist has no files outside the chosen root folder already.");
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  // applyMove starts the move in the background (a large or cross-drive
  // move can take a while — see internal/musicscanner.MoveArtist); the
  // effect below (gated on `moving`) polls status until it finishes,
  // same shape as the Scan status poll elsewhere in this app.
  const applyMove = () => {
    if (moveTargetId === "") return;
    setMoving(true);
    api
      .moveMusicArtist(artist.id, moveTargetId)
      .catch((err: unknown) => {
        setMoving(false);
        onError(String(err instanceof Error ? err.message : err));
      });
  };

  const remove = (deleteFiles: boolean) => {
    setBusy(true);
    api
      .removeMusicArtist(artist.id, deleteFiles)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const removeWanted = (wantedId: number) => {
    setRemovingWanted(true);
    api
      .removeWantedMusicAlbum(wantedId)
      .then(() => {
        setSelectedWantedId(null);
        refreshMissingAndWanted();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setRemovingWanted(false));
  };

  // Selecting a different wanted album always starts with releases hidden —
  // switching straight from one album's open ReleaseBrowser to another's
  // would otherwise carry the panel (and its auto-search) over silently.
  const selectWanted = (albumId: number) => {
    setSelectedWantedId((cur) => (cur === albumId ? null : albumId));
    setShowReleases(false);
  };

  const gridAlbums: GridAlbum[] = [
    ...albums.map((a) => ({
      kind: "owned" as const,
      key: `o${a.id}`,
      id: a.id,
      title: a.title,
      releaseDate: a.releaseDate,
      primaryType: a.primaryType,
      mbid: a.mbid,
    })),
    ...wanted.map((w) => ({
      kind: "wanted" as const,
      key: `w${w.id}`,
      id: w.id,
      title: w.title,
      releaseDate: w.releaseDate,
      primaryType: w.primaryType,
      status: w.status,
      releaseGroupMbid: w.releaseGroupMbid,
    })),
  ];
  const selectedWantedAlbum = wanted.find((w) => w.id === selectedWantedId) ?? null;

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← Music
      </button>

      <section className="card detail-head">
        {artist.imageUrl ? (
          <img className="detail-art" src={proxiedImage(artist.imageUrl)} alt={`Photo of ${artist.name}`} />
        ) : (
          <div className="detail-art fallback">{artist.name.charAt(0)}</div>
        )}
        <div className="detail-info">
          <h2>{artist.name}</h2>
          <p className="muted">
            {albums.length} album{albums.length === 1 ? "" : "s"} owned
            {wanted.length > 0 ? `, ${wanted.length} wanted` : ""}
          </p>
          {artist.bio && <p className="detail-desc">{artist.bio}</p>}
          {artist.mbid && (
            <div className="settings-actions detail-links">
              <a
                className="toggle"
                href={
                  artist.kind === "series"
                    ? `https://musicbrainz.org/series/${artist.mbid}`
                    : `https://musicbrainz.org/artist/${artist.mbid}`
                }
                target="_blank"
                rel="noreferrer"
                title={`Open this ${artist.kind === "series" ? "series" : "artist"} on MusicBrainz`}
              >
                MusicBrainz ↗
              </a>
              {artist.kind !== "series" && (
                <a
                  className="toggle"
                  href={`/api/v1/music/artist/${artist.id}/audiodb-link?apikey=${encodeURIComponent(getApiKey())}`}
                  target="_blank"
                  rel="noreferrer"
                  title="Open this artist on TheAudioDB (if it has one)"
                >
                  TheAudioDB ↗
                </a>
              )}
            </div>
          )}
          <div className="settings-actions">
            <button
              className={artist.isMonitored ? "toggle on" : "toggle"}
              disabled={busy}
              title="Whether this artist's discography is tracked for acquisition"
              onClick={toggleMonitor}
            >
              {artist.isMonitored ? "monitored" : "unmonitored"}
            </button>
            <button disabled={busy} onClick={refresh} title="Re-fetch discography and bio/photo from MusicBrainz/TheAudioDB">
              Refresh metadata
            </button>
            <button disabled={busy} onClick={scan} title="Scan music root folders for new files">
              Scan files
            </button>
            <button disabled={busy} onClick={previewOrganize} title="Preview naming-template moves for this artist's files only">
              Organize…
            </button>
            <button
              disabled={busy}
              onClick={() => setShowWriteTags(true)}
              title="Write this artist's matched metadata back into every owned file's own tags"
            >
              Write tags…
            </button>
          </div>
          <details className="disclosure">
            <summary>Advanced</summary>
            <div className="disclosure-body">
              <div className="settings-actions">
                {rootFolders.length > 1 && (
                  <select
                    value={moveTargetId}
                    disabled={busy || moving}
                    title="Move this artist's owned albums to a different root folder"
                    onChange={(e) => previewMove(e.target.value === "" ? "" : Number(e.target.value))}
                  >
                    <option value="">Move to…</option>
                    {rootFolders.map((f) => (
                      <option key={f.id} value={f.id}>
                        {f.name}
                      </option>
                    ))}
                  </select>
                )}
                <button className="danger" disabled={busy} onClick={() => setConfirmRemove(!confirmRemove)}>
                  Remove artist
                </button>
              </div>
            </div>
          </details>
          {notice && <p className="muted">{notice}</p>}
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
                <button disabled={busy} onClick={applyOrganize}>Apply</button>
                <button className="toggle" onClick={() => setRenamePlan(null)}>Cancel</button>
              </div>
            </div>
          )}
          {movePlan && movePlan.moves.length > 0 && (
            <div className="rename-plan">
              <p>
                <strong>{movePlan.moves.length}</strong> file(s),{" "}
                <strong>{formatBytes(movePlan.totalBytes)}</strong> will move to{" "}
                {rootFolders.find((f) => f.id === moveTargetId)?.name ?? "the chosen root folder"}. Files stay
                at the same relative path, just under the new root — this does not reorganize them.
              </p>
              <div className="settings-actions">
                <button disabled={moving} onClick={applyMove}>
                  {moving ? "Moving…" : "Move"}
                </button>
                <button className="toggle" disabled={moving} onClick={() => previewMove("")}>
                  Cancel
                </button>
              </div>
            </div>
          )}
          {moveResult && moveResult.error && (
            <p className="notice bad">Move failed: {moveResult.error}</p>
          )}
          {moveResult && !moveResult.error && (
            <p className={moveResult.errors.length ? "notice bad" : "notice ok"}>
              Moved {moveResult.moved} file(s)
              {moveResult.errors.length > 0 && ` — ${moveResult.errors.length} failed: ${moveResult.errors.join("; ")}`}
            </p>
          )}
          {confirmRemove && (
            <RemovePanel
              message={`Remove ${artist.name} from the music library? Owned albums/tracks are forgotten.`}
              checkboxLabel="Also delete their files from disk"
              busy={busy}
              onConfirm={remove}
              onCancel={() => setConfirmRemove(false)}
            />
          )}
        </div>
      </section>

      <section className="card">
        <div className="card-head">
          <h2>
            Albums ({albums.length}
            {wanted.length > 0 ? ` owned, ${wanted.length} wanted` : ""})
          </h2>
          <span className="row-actions">
            <span className="view-toggle">
              {(["grid", "compact", "list"] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  className={albumsView === v ? "toggle on" : "toggle"}
                  onClick={() => setAlbumsView(v)}
                  title={v === "grid" ? "Covers" : v === "compact" ? "Smaller covers" : "List"}
                >
                  {v === "grid" ? "Grid" : v === "compact" ? "Compact" : "List"}
                </button>
              ))}
            </span>
            {gridAlbums.length > 1 && (
              <>
                <SortSelect
                  value={albumsSort}
                  onChange={changeAlbumsSort}
                  options={[
                    ["date", "Release date"],
                    ["title", "Title"],
                  ]}
                />
                <DirectionButtons value={albumsDir} onChange={setAlbumsDir} />
              </>
            )}
          </span>
        </div>
        {gridAlbums.length === 0 ? (
          <p className="muted">
            Nothing owned or wanted yet — pick albums from <strong>Missing</strong>{" "}
            below, or scan a root folder with their files.
          </p>
        ) : albumsView === "list" ? (
          <ul className="rows">
            {sortGridAlbums(gridAlbums, albumsSort, albumsDir).map((g) => (
              <li key={g.key}>
                <div className="row">
                  <button
                    className="link"
                    onClick={() => (g.kind === "owned" ? onOpenAlbum(g.id) : selectWanted(g.id))}
                    title={g.kind === "wanted" ? "Show this wanted album's actions" : undefined}
                  >
                    {g.title}
                  </button>
                  <span className="row-actions">
                    <span className="muted">
                      {g.releaseDate ? g.releaseDate.slice(0, 4) : g.primaryType}
                    </span>
                    <span
                      className={
                        g.kind === "owned" ? "owned yes" : g.status === "downloading" ? "owned dl" : "owned no"
                      }
                    >
                      {g.kind === "owned" ? "owned" : g.status === "downloading" ? "downloading" : "wanted"}
                    </span>
                  </span>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className={albumsView === "compact" ? "poster-grid compact" : "poster-grid"}>
            {sortGridAlbums(gridAlbums, albumsSort, albumsDir).map((g) =>
              g.kind === "owned" ? (
                <button key={g.key} className="poster-card" onClick={() => onOpenAlbum(g.id)}>
                  <AlbumCover albumId={g.id} mbid={g.mbid} title={g.title} className="poster" />
                  <span className="poster-title">{g.title}</span>
                  <span className="poster-sub">
                    {g.releaseDate ? g.releaseDate.slice(0, 4) + " · " : ""}
                    <span className="owned yes">owned</span>
                  </span>
                </button>
              ) : (
                <button
                  key={g.key}
                  className={selectedWantedId === g.id ? "poster-card selected" : "poster-card"}
                  onClick={() => selectWanted(g.id)}
                  title="Show this wanted album's actions"
                >
                  <WantedPoster releaseGroupMbid={g.releaseGroupMbid} title={g.title} />
                  <span className="poster-title">{g.title}</span>
                  <span className="poster-sub">
                    {g.releaseDate ? g.releaseDate.slice(0, 4) + " · " : ""}
                    <span className={g.status === "downloading" ? "owned dl" : "owned no"}>
                      {g.status === "downloading" ? "downloading" : "wanted"}
                    </span>
                  </span>
                </button>
              ),
            )}
          </div>
        )}
        {selectedWantedAlbum && (
          <div className="missing-detail">
            <div className="row">
              <strong>{selectedWantedAlbum.title}</strong>
              <span className="row-actions">
                <button
                  className={showReleases ? "toggle on" : ""}
                  onClick={() => setShowReleases(!showReleases)}
                  title="Browse every release candidate — sort, filter, pick one yourself"
                >
                  {showReleases ? "Hide releases" : "Search releases"}
                </button>
                <button
                  className="toggle"
                  disabled={removingWanted}
                  title="Stop wanting this album — it moves back to Missing"
                  onClick={() => removeWanted(selectedWantedAlbum.id)}
                >
                  Stop wanting
                </button>
              </span>
            </div>
            {showReleases && (
              <ReleaseBrowser
                wantedAlbumId={selectedWantedAlbum.id}
                onGrabbed={refreshMissingAndWanted}
                onClose={() => setShowReleases(false)}
              />
            )}
          </div>
        )}
      </section>

      <MissingAlbumsCard artistId={id} onChanged={refreshMissingAndWanted} onError={onError} refreshKey={reloadTick} />
      {showWriteTags && (
        <WriteTagsDialog scope="artist" onConfirm={confirmWriteTags} onClose={() => setShowWriteTags(false)} />
      )}
    </>
  );
}

// MissingAlbumsCard lists the artist's cached discography gaps — release
// groups from MusicBrainz that are neither owned nor already wanted.
function MissingAlbumsCard({
  artistId,
  onChanged,
  onError,
  refreshKey,
}: {
  artistId: number;
  onChanged: () => void;
  onError: (message: string) => void;
  refreshKey: number;
}) {
  const [missing, setMissing] = useState<MusicReleaseGroup[] | null>(null);
  const [busyMbid, setBusyMbid] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState("date");
  const [dir, setDir] = useState<SortDir>(defaultDirFor("date"));
  const [openMbid, setOpenMbid] = useState<string | null>(null);
  const changeSort = (key: string) => {
    setSort(key);
    setDir(defaultDirFor(key));
  };

  useEffect(() => {
    api
      .listMissingMusicReleaseGroups(artistId)
      .then(setMissing)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [artistId, onError, refreshKey]);

  if (!missing) return null;

  const add = (rg: MusicReleaseGroup, monitor: boolean) => {
    setBusyMbid(rg.releaseGroupMbid);
    api
      .wantMusicAlbum(artistId, rg.releaseGroupMbid, monitor)
      .then(onChanged)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyMbid(null));
  };

  const filtered = missing.filter((rg) => rg.title.toLowerCase().includes(filter.toLowerCase()));
  // Grouped by release type (Album on top, then EP/Single/Live/...) —
  // sorting only reorders items *within* a group, never the groups
  // themselves, since sortReleaseGroups runs before the stable partition.
  const groups = groupByReleaseCategory(sortReleaseGroups(filtered, sort, dir), (rg) =>
    releaseCategory(rg.primaryType, rg.secondaryTypes),
  );

  return (
    <section className="card">
      <div className="card-head">
        <h2>Missing ({missing.length})</h2>
        {missing.length > 1 && (
          <span className="row-actions">
            <SortSelect
              value={sort}
              onChange={changeSort}
              options={[
                ["date", "Release date"],
                ["title", "Title"],
              ]}
            />
            <DirectionButtons value={dir} onChange={setDir} />
          </span>
        )}
      </div>
      {missing.length === 0 ? (
        <p className="muted">
          No gaps — every cached release group is owned or already wanted. Try{" "}
          <strong>Refresh metadata</strong> above to pick up new releases.
        </p>
      ) : (
        <>
          {missing.length > 10 && (
            <input
              className="grid-filter"
              placeholder="Filter missing albums…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          )}
          {filtered.length === 0 && <p className="muted">No missing albums match the filter.</p>}
          {groups.map((g) => (
            <div key={g.category}>
              {groups.length > 1 && (
                <h3 className="group-heading">
                  {g.category} ({g.items.length})
                </h3>
              )}
              <ul className="rows">
                {g.items.map((rg) => (
                  <li key={rg.releaseGroupMbid}>
                    <div className="row">
                      <span>
                        <button
                          className="link"
                          onClick={() =>
                            setOpenMbid(openMbid === rg.releaseGroupMbid ? null : rg.releaseGroupMbid)
                          }
                          title="Show this album's tracklist"
                        >
                          {openMbid === rg.releaseGroupMbid ? "▾" : "▸"} {rg.title}
                        </button>
                        <span className="muted">
                          {" "}
                          {rg.primaryType}
                          {rg.firstReleaseDate ? ` · ${rg.firstReleaseDate.slice(0, 4)}` : ""}
                        </span>
                      </span>
                      <span className="row-actions">
                        <button
                          disabled={busyMbid !== null}
                          title="Want this album without monitoring the artist further"
                          onClick={() => add(rg, false)}
                        >
                          + Add
                        </button>
                        <button
                          disabled={busyMbid !== null}
                          title="Want this album and monitor the artist"
                          onClick={() => add(rg, true)}
                        >
                          + Add &amp; Monitor
                        </button>
                      </span>
                    </div>
                    {openMbid === rg.releaseGroupMbid && (
                      <div className="missing-detail">
                        <ReleaseTracklistPreview releaseGroupMbid={rg.releaseGroupMbid} />
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </>
      )}
    </section>
  );
}

// ReleaseTracklistPreview fetches and shows a release group's tracklist
// straight from MusicBrainz — used by both Missing and Wanted rows, which
// have no local album/track rows to read from yet (nothing scanned, so
// nothing to slot a track file into). Self-contained: owns its own
// loading/error state rather than bubbling errors up to the page's toast,
// since a failed preview here shouldn't interrupt anything else in progress.
function ReleaseTracklistPreview({ releaseGroupMbid }: { releaseGroupMbid: string }) {
  const [data, setData] = useState<ReleaseGroupTracklist | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    setData(null);
    api
      .getReleaseGroupTracks(releaseGroupMbid)
      .then((d) => {
        if (!cancelled) setData(d);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(String(err instanceof Error ? err.message : err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [releaseGroupMbid]);

  if (loading) return <p className="muted">Loading tracklist…</p>;
  if (error) return <p className="muted">Couldn't load tracklist: {error}</p>;
  if (!data || data.tracks.length === 0) return <p className="muted">No tracklist available.</p>;

  const multiDisc = data.tracks.some((t) => t.discNumber > 1);
  return (
    <ul className="rows nested">
      {data.tracks.map((t) => (
        <li key={`${t.discNumber}-${t.position}`}>
          <div className="row">
            <span>
              {multiDisc ? `${t.discNumber}.` : ""}
              {String(t.position).padStart(2, "0")} — {t.title}
            </span>
            <span className="muted">{formatDuration(t.durationMs)}</span>
          </div>
        </li>
      ))}
    </ul>
  );
}

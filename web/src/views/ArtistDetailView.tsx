import { useCallback, useEffect, useState } from "react";
import {
  api,
  musicAlbumCoverUrl,
  proxiedImage,
  type MusicAlbum,
  type MusicArtist,
  type MusicReleaseGroup,
  type ReleaseGroupTracklist,
  type RenameMove,
  type WantedAlbum,
} from "../api";
import RemovePanel from "../components/RemovePanel";
import { DetailSkeleton } from "../components/Skeleton";
import {
  SortSelect,
  DirectionButtons,
  sortAlbums,
  sortReleaseGroups,
  releaseCategory,
  groupByReleaseCategory,
  defaultDirFor,
  type SortDir,
} from "../components/SortControl";
import { formatDuration } from "../format";

// Albums section display: "grid" (current default, large covers), "compact"
// (same grid, smaller covers), or "list" (a plain title + status row).
type AlbumsView = "grid" | "compact" | "list";

// Full-page artist detail, mirroring the author page: header with portrait,
// bio and artist-level actions, then owned albums as a cover grid, a
// Missing section (cached discography not yet owned/wanted), and a Wanted
// section (albums queued for search/grab).
export default function ArtistDetailView({
  id,
  onError,
  onBack,
  onOpenAlbum,
}: {
  id: number;
  onError: (message: string) => void;
  onBack: () => void;
  onOpenAlbum: (albumId: number) => void;
}) {
  const [artist, setArtist] = useState<MusicArtist | null>(null);
  const [albums, setAlbums] = useState<MusicAlbum[]>([]);
  const [busy, setBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [notice, setNotice] = useState("");
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [reloadTick, setReloadTick] = useState(0);
  const [albumsSort, setAlbumsSort] = useState("date");
  const [albumsDir, setAlbumsDir] = useState<SortDir>(defaultDirFor("date"));
  const [albumsView, setAlbumsView] = useState<AlbumsView>("grid");
  const changeAlbumsSort = (key: string) => {
    setAlbumsSort(key);
    setAlbumsDir(defaultDirFor(key));
  };

  const reload = useCallback(() => {
    Promise.all([api.getMusicArtist(id), api.listMusicAlbums(id)])
      .then(([a, al]) => {
        setArtist(a);
        setAlbums(al);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [id, onError]);

  useEffect(reload, [reload]);

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

  const remove = (deleteFiles: boolean) => {
    setBusy(true);
    api
      .removeMusicArtist(artist.id, deleteFiles)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

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
          </p>
          {artist.bio && <p className="detail-desc">{artist.bio}</p>}
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
            <button className="danger" disabled={busy} onClick={() => setConfirmRemove(!confirmRemove)}>
              Remove artist
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
                <button disabled={busy} onClick={applyOrganize}>Apply</button>
                <button className="toggle" onClick={() => setRenamePlan(null)}>Cancel</button>
              </div>
            </div>
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
          <h2>Albums ({albums.length})</h2>
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
            {albums.length > 1 && (
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
        {albums.length === 0 ? (
          <p className="muted">
            Nothing owned yet — pick albums to want from <strong>Missing</strong>{" "}
            below, or scan a root folder with their files.
          </p>
        ) : albumsView === "list" ? (
          <ul className="rows">
            {sortAlbums(albums, albumsSort, albumsDir).map((al) => (
              <li key={al.id}>
                <div className="row">
                  <button className="link" onClick={() => onOpenAlbum(al.id)}>
                    {al.title}
                  </button>
                  <span className="muted">
                    {al.releaseDate ? al.releaseDate.slice(0, 4) : al.primaryType}
                  </span>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className={albumsView === "compact" ? "poster-grid compact" : "poster-grid"}>
            {sortAlbums(albums, albumsSort, albumsDir).map((al) => (
              <button key={al.id} className="poster-card" onClick={() => onOpenAlbum(al.id)}>
                {al.mbid ? (
                  <img className="poster" src={musicAlbumCoverUrl(al.id)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{al.title.charAt(0)}</div>
                )}
                <span className="poster-title">{al.title}</span>
                <span className="poster-sub">
                  {al.releaseDate ? al.releaseDate.slice(0, 4) : al.primaryType}
                </span>
              </button>
            ))}
          </div>
        )}
      </section>

      <WantedAlbumsCard artistId={id} onError={onError} refreshKey={reloadTick} />
      <MissingAlbumsCard artistId={id} onMonitored={reload} onError={onError} refreshKey={reloadTick} />
    </>
  );
}

// WantedAlbumsCard lists albums queued for acquisition — search indexers and
// grab, or ignore.
function WantedAlbumsCard({
  artistId,
  onError,
  refreshKey,
}: {
  artistId: number;
  onError: (message: string) => void;
  refreshKey: number;
}) {
  const [wanted, setWanted] = useState<WantedAlbum[]>([]);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [releases, setReleases] = useState<Record<number, { title: string; downloadUrl: string; guid: string; protocol: string; indexer: string; size: number }[]>>({});
  const [openMbid, setOpenMbid] = useState<string | null>(null);

  const reload = useCallback(() => {
    api
      .listWantedMusicAlbums(artistId)
      .then((w) => setWanted(w.filter((x) => x.status !== "ignored")))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [artistId, onError]);

  useEffect(reload, [reload, refreshKey]);

  if (wanted.length === 0) return null;

  const search = (w: WantedAlbum) => {
    setBusyId(w.id);
    api
      .searchWantedMusicAlbum(w.id)
      .then((r) => setReleases((prev) => ({ ...prev, [w.id]: r.releases })))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyId(null));
  };

  const grab = (w: WantedAlbum, rel: { title: string; downloadUrl: string; guid: string; protocol: string }) => {
    setBusyId(w.id);
    api
      .grabWantedMusicAlbum(w.id, rel.title, rel.downloadUrl, rel.protocol, rel.guid)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyId(null));
  };

  const ignore = (w: WantedAlbum) => {
    setBusyId(w.id);
    api
      .ignoreWantedMusicAlbum(w.id)
      .then(reload)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusyId(null));
  };

  return (
    <section className="card">
      <h2>Wanted ({wanted.length})</h2>
      <ul className="rows">
        {wanted.map((w) => (
          <li key={w.id}>
            <div className="row">
              <span>
                <button
                  className="link"
                  onClick={() => setOpenMbid(openMbid === w.releaseGroupMbid ? null : w.releaseGroupMbid)}
                  title="Show this album's tracklist"
                >
                  {openMbid === w.releaseGroupMbid ? "▾" : "▸"} {w.title}
                </button>
                <span className="muted"> · {w.status}</span>
              </span>
              <span className="row-actions">
                <button disabled={busyId !== null} onClick={() => search(w)}>
                  {busyId === w.id ? "Searching…" : "Search releases"}
                </button>
                <button disabled={busyId !== null} className="toggle" onClick={() => ignore(w)}>
                  Ignore
                </button>
              </span>
            </div>
            {openMbid === w.releaseGroupMbid && (
              <div className="missing-detail">
                <ReleaseTracklistPreview releaseGroupMbid={w.releaseGroupMbid} />
              </div>
            )}
            {releases[w.id] && (
              <ul className="rows nested">
                {releases[w.id].length === 0 && <li className="muted">No releases found.</li>}
                {releases[w.id].map((r, i) => (
                  <li key={i}>
                    <div className="row">
                      <span>
                        {r.title} <span className="muted">· {r.indexer}</span>
                      </span>
                      <button disabled={busyId !== null} onClick={() => grab(w, r)}>
                        Grab
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

// MissingAlbumsCard lists the artist's cached discography gaps — release
// groups from MusicBrainz that are neither owned nor already wanted.
function MissingAlbumsCard({
  artistId,
  onMonitored,
  onError,
  refreshKey,
}: {
  artistId: number;
  onMonitored: () => void;
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
      .then(onMonitored)
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

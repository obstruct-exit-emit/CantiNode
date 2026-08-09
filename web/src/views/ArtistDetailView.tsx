import { useCallback, useEffect, useState } from "react";
import {
  api,
  musicAlbumCoverUrl,
  proxiedImage,
  type MusicAlbum,
  type MusicArtist,
  type MusicReleaseGroup,
  type RenameMove,
  type WantedAlbum,
} from "../api";
import RemovePanel from "../components/RemovePanel";
import { DetailSkeleton } from "../components/Skeleton";

// Full-page artist detail, mirroring the author page: header with portrait,
// bio and artist-level actions, then owned albums as a cover grid, a
// Missing section (cached discography not yet owned/wanted), and a Wanted
// section (albums queued for search/grab).
export default function ArtistDetailView({
  id,
  onError,
  onBack,
}: {
  id: number;
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const [artist, setArtist] = useState<MusicArtist | null>(null);
  const [albums, setAlbums] = useState<MusicAlbum[]>([]);
  const [busy, setBusy] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [notice, setNotice] = useState("");
  const [renamePlan, setRenamePlan] = useState<RenameMove[] | null>(null);
  const [reloadTick, setReloadTick] = useState(0);

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
        <h2>Albums ({albums.length})</h2>
        {albums.length === 0 ? (
          <p className="muted">
            Nothing owned yet — pick albums to want from <strong>Missing</strong>{" "}
            below, or scan a root folder with their files.
          </p>
        ) : (
          <div className="poster-grid">
            {albums.map((al) => (
              <div key={al.id} className="poster-card">
                {al.mbid ? (
                  <img className="poster" src={musicAlbumCoverUrl(al.id)} alt="" loading="lazy" />
                ) : (
                  <div className="poster fallback">{al.title.charAt(0)}</div>
                )}
                <span className="poster-title">{al.title}</span>
                <span className="poster-sub">
                  {al.releaseDate ? al.releaseDate.slice(0, 4) : al.primaryType}
                </span>
              </div>
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
                {w.title}
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

  return (
    <section className="card">
      <h2>Missing ({missing.length})</h2>
      {missing.length === 0 ? (
        <p className="muted">
          No gaps — every cached release group is owned or already wanted. Try{" "}
          <strong>Refresh metadata</strong> above to pick up new releases.
        </p>
      ) : (
        <ul className="rows">
          {missing.map((rg) => (
            <li key={rg.releaseGroupMbid}>
              <div className="row">
                <span>
                  {rg.title}
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
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

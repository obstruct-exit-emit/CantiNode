import { useCallback, useEffect, useState } from "react";
import { api, musicReleaseGroupCoverUrl, type ReleaseGroupTracklist, type WantedAlbum } from "../api";
import ReleaseBrowser from "../components/ReleaseBrowser";
import { DetailSkeleton } from "../components/Skeleton";
import { formatDuration } from "../format";

// Full-page detail for an album that isn't owned yet — the wanted grid's
// counterpart to AlbumDetailView, deliberately mirroring its layout (same
// detail-head/detail-info/detail-stats shell, same Tracks card) so opening
// a wanted album feels like opening an owned one, just with Search
// releases/Stop wanting in place of Scan/Organize/Remove and a "wanted"
// flag on every track instead of file info. The tracklist itself comes
// from the release group's own cached representative release (see
// internal/metadatabackfill.pickRepresentativeRelease — prefers a US CD
// edition, the closest thing to "what you'll actually end up owning," as
// its own doc comment explains) — once a real file is actually matched,
// the album moves to the owned grid and AlbumDetailView takes over with
// that file's own real release instead; this page only ever governs the
// preview shown before that happens.
export default function WantedAlbumDetailView({
  id,
  artistId,
  onError,
  onBack,
}: {
  id: number;
  artistId: number;
  onError: (message: string) => void;
  onBack: () => void;
}) {
  const [wanted, setWanted] = useState<WantedAlbum | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [tracklist, setTracklist] = useState<ReleaseGroupTracklist | null>(null);
  const [tracklistError, setTracklistError] = useState("");
  const [showReleases, setShowReleases] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [coverFailed, setCoverFailed] = useState(false);

  // There's no single-item "get wanted album" endpoint — this page reuses
  // the same per-artist list ArtistDetailView already fetches, since it
  // already knows the artist id from the route (#/wanted/17?artist=12).
  const reload = useCallback(() => {
    api
      .listWantedMusicAlbums(artistId)
      .then((list) => {
        const w = list.find((x) => x.id === id) ?? null;
        setWanted(w);
        setNotFound(!w);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [artistId, id, onError]);

  useEffect(reload, [reload]);

  useEffect(() => {
    if (!wanted) return;
    let cancelled = false;
    setTracklistError("");
    api
      .getReleaseGroupTracks(wanted.releaseGroupMbid)
      .then((d) => {
        if (!cancelled) setTracklist(d);
      })
      .catch((err: unknown) => {
        if (!cancelled) setTracklistError(String(err instanceof Error ? err.message : err));
      });
    return () => {
      cancelled = true;
    };
  }, [wanted]);

  const stopWanting = () => {
    if (!wanted) return;
    setRemoving(true);
    api
      .removeWantedMusicAlbum(wanted.id)
      .then(onBack)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setRemoving(false));
  };

  if (notFound) {
    return (
      <>
        <button className="link back" onClick={onBack}>
          ← Artist
        </button>
        <p className="muted">
          This album isn't wanted anymore — it may have just been matched to a real file. Check the
          artist page.
        </p>
      </>
    );
  }

  if (!wanted) return <DetailSkeleton />;

  const year = wanted.releaseDate ? wanted.releaseDate.slice(0, 4) : "";
  const trackCount = tracklist?.tracks.length ?? 0;
  const isMultiDisc = (tracklist?.tracks ?? []).some((t) => t.discNumber > 1);

  return (
    <>
      <button className="link back" onClick={onBack}>
        ← Artist
      </button>

      <section className="card detail-head">
        {coverFailed || !wanted.releaseGroupMbid ? (
          <div className="detail-art fallback">{wanted.title.charAt(0)}</div>
        ) : (
          <img
            className="detail-art"
            src={musicReleaseGroupCoverUrl(wanted.releaseGroupMbid)}
            alt={`Cover of ${wanted.title}`}
            loading="lazy"
            onError={() => setCoverFailed(true)}
          />
        )}
        <div className="detail-info">
          <h2>
            {wanted.title}
            {year && <span className="muted"> ({year})</span>}
          </h2>
          <p className="muted">
            {wanted.primaryType || "Album"}
            {tracklist ? ` · ${trackCount} track${trackCount === 1 ? "" : "s"}` : ""}
          </p>
          <div className="detail-stats">
            <div className="detail-stat">
              <span className="detail-stat-label">Status</span>
              <span className={`detail-stat-value ${wanted.status === "downloading" ? "owned dl" : "owned no"}`}>
                {wanted.status === "downloading" ? "downloading" : "wanted"}
              </span>
            </div>
          </div>
          {wanted.releaseGroupMbid && (
            <div className="settings-actions detail-links">
              <a
                className="toggle"
                href={`https://musicbrainz.org/release-group/${wanted.releaseGroupMbid}`}
                target="_blank"
                rel="noreferrer"
                title="Open this release group on MusicBrainz"
              >
                MusicBrainz ↗
              </a>
            </div>
          )}
          <div className="settings-actions">
            <button
              className={showReleases ? "toggle on" : ""}
              onClick={() => setShowReleases(!showReleases)}
              title="Browse every release candidate — sort, filter, pick one yourself"
            >
              {showReleases ? "Hide releases" : "Search releases"}
            </button>
            <button
              className="toggle"
              disabled={removing}
              title="Stop wanting this album — it moves back to Missing"
              onClick={stopWanting}
            >
              Stop wanting
            </button>
          </div>
          {showReleases && (
            <ReleaseBrowser wantedAlbumId={wanted.id} onGrabbed={reload} onClose={() => setShowReleases(false)} />
          )}
        </div>
      </section>

      <section className="card">
        <h2>Tracks {tracklist ? `(${trackCount})` : ""}</h2>
        {tracklistError ? (
          <p className="muted">Couldn't load tracklist: {tracklistError}</p>
        ) : !tracklist ? (
          <p className="muted">Loading tracklist…</p>
        ) : trackCount === 0 ? (
          <p className="muted">No tracklist available yet.</p>
        ) : (
          <ul className="rows">
            {tracklist.tracks.map((t) => (
              <li key={`${t.discNumber}-${t.position}`}>
                <div className="row">
                  <span>
                    {isMultiDisc ? `${t.discNumber}.` : ""}
                    {String(t.position).padStart(2, "0")} — {t.title}
                  </span>
                  <span className="track-row-actions">
                    <span className="muted">{formatDuration(t.durationMs)}</span>
                    <span className="col-owned owned no">wanted</span>
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  );
}

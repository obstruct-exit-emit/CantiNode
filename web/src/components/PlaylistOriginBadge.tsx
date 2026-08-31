import { PLAYLIST_ORIGIN_PLEX, type Playlist } from "../api";

// PlaylistOriginBadge answers two different questions in one small pill:
// where a playlist first came from (its label — "CantiNode" or "Plex",
// permanent, never changes) and whether it's currently linked and
// syncing to Plex (its own accent styling — plexRatingKey can come and
// go independently of origin, e.g. a Plex-origin playlist whose Plex
// copy was later deleted under "unlink" mode keeps its Plex label but
// loses the link).
export default function PlaylistOriginBadge({ playlist: p }: { playlist: Playlist }) {
  const linked = !!p.plexRatingKey;
  const fromPlex = p.origin === PLAYLIST_ORIGIN_PLEX;
  const title = linked
    ? `Synced with Plex${p.plexSyncedAt ? ` — last synced ${new Date(p.plexSyncedAt).toLocaleString()}` : ""}`
    : fromPlex
      ? "Originally pulled in from Plex — no longer linked (its Plex copy was deleted)"
      : "Created in CantiNode — not linked to Plex";
  return (
    <span className={`pill playlist-origin${linked ? " playlist-linked" : ""}`} title={title}>
      {linked ? "🔌 " : ""}
      {fromPlex ? "Plex" : "CantiNode"}
    </span>
  );
}

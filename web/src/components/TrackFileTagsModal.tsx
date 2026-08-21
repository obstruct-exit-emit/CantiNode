import { useEffect, useState } from "react";
import { api, type MusicTrackFileTags } from "../api";

// TrackFileTagsModal shows one track file's own embedded tags, read live
// off disk (see getMusicTrackFileTags) rather than whatever's cached in
// the database — the point being to answer "what does this file actually
// have on it right now," which the cached snapshot can't always answer
// (it goes stale after a "Write tags" call).
export default function TrackFileTagsModal({
  fileId,
  fileName,
  onClose,
}: {
  fileId: number;
  fileName: string;
  onClose: () => void;
}) {
  const [tags, setTags] = useState<MusicTrackFileTags | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    api
      .getMusicTrackFileTags(fileId)
      .then((t) => {
        if (!cancelled) setTags(t);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [fileId]);

  const fields: [string, string][] = tags
    ? [
        ["Title", tags.title],
        ["Artist", tags.artist],
        ["Album Artist", tags.albumArtist],
        ["Album", tags.album],
        ["Track / Disc", `${tags.trackNumber || "—"} / ${tags.discNumber || "—"}`],
        ["Year", tags.year ? String(tags.year) : ""],
        ["Format", tags.format],
        ["MusicBrainz Artist ID", tags.musicBrainzArtistId],
        ["MusicBrainz Album ID", tags.musicBrainzAlbumId],
        ["MusicBrainz Release Group ID", tags.musicBrainzReleaseGroupId],
        ["MusicBrainz Recording ID", tags.musicBrainzRecordingId],
      ]
    : [];

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal tag-modal"
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        <h3>Embedded tags</h3>
        <p className="muted tag-modal-filename" title={fileName}>
          {fileName}
        </p>
        {loading && <p className="muted">Reading tags…</p>}
        {error && <p className="notice bad">{error}</p>}
        {!loading && !error && (
          <dl className="tag-fields">
            {fields.map(([label, value]) => (
              <div className="tag-field" key={label}>
                <dt>{label}</dt>
                <dd>{value || <span className="muted">—</span>}</dd>
              </div>
            ))}
          </dl>
        )}
        <div className="settings-actions">
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}

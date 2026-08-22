// TrackCreditsModal lists every name in a track's own artist-credit —
// names only for now, in the order MusicBrainz's own artist-credit
// carries them. There's no role/credit-type data ("lead vocals",
// "producer", ...) behind this yet: that would need a separate
// recording-level relationship fetch from MusicBrainz (the same kind of
// call internal/musicbrainz.Recording.Composer already makes for
// work-relations, just at the recording level instead) that nothing in
// the matching/scanning pipeline requests today — a real follow-up
// feature, not something this modal can show from data CantiNode
// already has.
export default function TrackCreditsModal({
  trackTitle,
  names,
  onClose,
}: {
  trackTitle: string;
  names: string[];
  onClose: () => void;
}) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3>Credits</h3>
        <p className="muted tag-modal-filename" title={trackTitle}>
          {trackTitle}
        </p>
        <ul className="credits-list">
          {names.map((name, i) => (
            <li key={i}>{name}</li>
          ))}
        </ul>
        <div className="settings-actions">
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}

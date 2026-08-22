// TrackCreditsModal lists a track's featured guests — the credit's
// primary artist (already the album's own artist, whenever this even
// renders) is dropped by the caller before this ever sees it, so every
// name here is someone else the recording is credited to. Names only for
// now, in the order MusicBrainz's own artist-credit carries them. There's
// no role/credit-type data ("lead vocals", "producer", ...) behind this
// yet: that would need a separate recording-level relationship fetch
// from MusicBrainz (the same kind of call
// internal/musicbrainz.Recording.Composer already makes for
// work-relations, just at the recording level instead) that nothing in
// the matching/scanning pipeline requests today — a real follow-up
// feature, not something this modal can show from data CantiNode
// already has.
//
// Each name gets a Wikipedia link, the same "toggle" pill style as the
// MusicBrainz/TheAudioDB links on the artist/album pages — a direct
// title guess, not a search, same as those two link straight to a known
// ID rather than a search result. There's no MBID per credited name
// here (joinArtistCredit already flattened that away), so unlike those
// two this can land on a disambiguation or "no exact match" page for an
// ambiguous name; Wikipedia's own page handles that gracefully. Room to
// add more per-name link providers later, the same way the header links
// could grow past just these two.
function wikipediaURL(name: string): string {
  return `https://en.wikipedia.org/wiki/${encodeURIComponent(name.replace(/ /g, "_"))}`;
}

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
        <h3>Featuring</h3>
        <p className="muted tag-modal-filename" title={trackTitle}>
          {trackTitle}
        </p>
        <ul className="credits-list">
          {names.map((name, i) => (
            <li key={i}>
              <span>{name}</span>
              <a className="toggle" href={wikipediaURL(name)} target="_blank" rel="noreferrer" title={`Search Wikipedia for ${name}`}>
                Wikipedia ↗
              </a>
            </li>
          ))}
        </ul>
        <div className="settings-actions">
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}

import { useState } from "react";

// WriteTagsDialog replaces two separate buttons ("Write tags" and "Write
// tags (clear first)") with one button that opens this options window —
// merge is still the default/pre-selected choice, so the common case is
// still just two clicks (open, confirm), but clear-first no longer needs
// its own separate entry point in the header's action row.
export default function WriteTagsDialog({
  scope,
  onConfirm,
  onClose,
}: {
  // Only changes the clear-first warning's wording — "this album's files"
  // vs "every album this artist owns".
  scope: "album" | "artist";
  onConfirm: (clear: boolean) => void;
  onClose: () => void;
}) {
  const [clear, setClear] = useState(false);

  const scopeText = scope === "artist" ? "across every album this artist owns" : "for this album";

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal write-tags-modal" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h3>Write tags</h3>
        <div className="write-tags-options">
          <label className={`write-tags-option${clear ? "" : " selected"}`}>
            <input type="radio" name="write-tags-mode" checked={!clear} onChange={() => setClear(false)} />
            <div>
              <div className="write-tags-option-title">Merge (recommended)</div>
              <p className="muted">
                Only touches the fields CantiNode itself manages — title, artist, genre, composer, cover art,
                MusicBrainz IDs, and the rest. Everything else already on the file (comments, lyrics, ReplayGain,
                ratings, custom fields from other taggers) is left untouched.
              </p>
            </div>
          </label>
          <label className={`write-tags-option${clear ? " selected" : ""}`}>
            <input type="radio" name="write-tags-mode" checked={clear} onChange={() => setClear(true)} />
            <div>
              <div className="write-tags-option-title">Clear first</div>
              <p className="muted">
                Wipes every tag CantiNode doesn't itself manage {scopeText} before writing — embedded cover art (a
                fresh one is re-embedded from the cached cover, if available), comments, lyrics, ReplayGain,
                ratings, custom fields from other taggers. <strong>This cannot be undone.</strong>
              </p>
            </div>
          </label>
        </div>
        <div className="settings-actions">
          <button className={clear ? "danger" : undefined} autoFocus onClick={() => onConfirm(clear)}>
            {clear ? "Clear and write" : "Write tags"}
          </button>
          <button className="toggle" onClick={onClose}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}

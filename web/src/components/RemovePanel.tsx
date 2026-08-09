import { useState } from "react";

// RemovePanel is the shared removal confirmation: a message, an opt-in
// "also delete files from disk" checkbox (always unchecked to start), and
// Remove/Cancel. Used by author/series detail headers and book rows.
export default function RemovePanel({
  message,
  checkboxLabel,
  busy,
  onConfirm,
  onCancel,
}: {
  message: string;
  checkboxLabel: string;
  busy: boolean;
  onConfirm: (deleteFiles: boolean) => void;
  onCancel: () => void;
}) {
  const [deleteFiles, setDeleteFiles] = useState(false);

  return (
    <div className="remove-panel">
      <p>{message}</p>
      <label className="check">
        <input
          type="checkbox"
          checked={deleteFiles}
          onChange={(e) => setDeleteFiles(e.target.checked)}
        />{" "}
        {checkboxLabel}
      </label>
      <div className="settings-actions">
        <button className="danger" disabled={busy} onClick={() => onConfirm(deleteFiles)}>
          {deleteFiles ? "Remove & delete files" : "Remove"}
        </button>
        <button className="toggle" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

import { useState } from "react";
import { useUi } from "../ui";

// RemovePanel is the shared removal confirmation: a message, an opt-in
// "also delete files from disk" checkbox (always unchecked to start), and
// Remove/Cancel. Used by author/series detail headers and book rows.
//
// Checking the box and clicking through used to go straight to deletion —
// this panel is already itself a confirmation step, but "also delete from
// disk" is a second, irreversible commitment layered on top of it, easy to
// have checked without quite registering (it defaults off, but nothing
// stops a quick click-checkbox-then-click-button sequence). A final
// danger-styled confirmDlg now stands between the click and the actual
// call whenever the checkbox is on — the same modal pattern ActivityView's
// "Remove download" already uses for its own destructive action, not a
// new one invented here.
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
  const { confirmDlg } = useUi();

  const handleConfirm = async () => {
    if (deleteFiles) {
      const ok = await confirmDlg({
        title: "Delete files from disk?",
        message:
          "This permanently deletes the files themselves, not just the library entry. There is no undo.",
        confirmLabel: "Delete files",
        danger: true,
      });
      if (!ok) return;
    }
    onConfirm(deleteFiles);
  };

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
        <button className="danger" disabled={busy} onClick={handleConfirm}>
          {deleteFiles ? "Remove & delete files" : "Remove"}
        </button>
        <button className="toggle" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

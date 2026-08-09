import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

// The app-wide UI layer: stacking toasts and a styled confirm dialog. One
// consistent voice for feedback — replaces the scattered inline notice
// formats and every native browser confirm() popup.

export interface ConfirmOptions {
  title?: string;
  message: string;
  confirmLabel?: string;
  // Danger styles the confirm button red — destructive actions only.
  danger?: boolean;
}

type ToastKind = "ok" | "bad" | "info";

interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

interface UiContextValue {
  toast: (message: string, kind?: ToastKind) => void;
  confirmDlg: (opts: ConfirmOptions) => Promise<boolean>;
}

const UiContext = createContext<UiContextValue | null>(null);

export function useUi(): UiContextValue {
  const v = useContext(UiContext);
  if (!v) throw new Error("useUi must be used inside UiProvider");
  return v;
}

export function UiProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [confirm, setConfirm] = useState<
    (ConfirmOptions & { resolve: (ok: boolean) => void }) | null
  >(null);
  const nextID = useRef(1);

  const dismiss = useCallback(
    (id: number) => setToasts((t) => t.filter((x) => x.id !== id)),
    [],
  );

  const toast = useCallback(
    (message: string, kind: ToastKind = "info") => {
      const id = nextID.current++;
      // Keep the stack short — a fifth toast pushes the oldest out.
      setToasts((t) => [...t.slice(-3), { id, kind, message }]);
      window.setTimeout(() => dismiss(id), kind === "bad" ? 8000 : 5000);
    },
    [dismiss],
  );

  const confirmDlg = useCallback(
    (opts: ConfirmOptions) =>
      new Promise<boolean>((resolve) => setConfirm({ ...opts, resolve })),
    [],
  );

  const settle = useCallback(
    (ok: boolean) => {
      setConfirm((c) => {
        c?.resolve(ok);
        return null;
      });
    },
    [],
  );

  // Escape closes the dialog from anywhere.
  useEffect(() => {
    if (!confirm) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") settle(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [confirm, settle]);

  return (
    <UiContext.Provider value={{ toast, confirmDlg }}>
      {children}
      {toasts.length > 0 && (
        <div className="toasts">
          {toasts.map((t) => (
            <div key={t.id} className={`toast ${t.kind}`} role="status">
              <span className="toast-msg">{t.message}</span>
              <button className="toast-x" aria-label="Dismiss" onClick={() => dismiss(t.id)}>
                ✕
              </button>
            </div>
          ))}
        </div>
      )}
      {confirm && (
        <div className="modal-backdrop" onClick={() => settle(false)}>
          <div
            className="modal"
            role="dialog"
            aria-modal="true"
            onClick={(e) => e.stopPropagation()}
          >
            {confirm.title && <h3>{confirm.title}</h3>}
            <p className="modal-msg">{confirm.message}</p>
            <div className="settings-actions">
              <button
                className={confirm.danger ? "danger" : undefined}
                autoFocus
                onClick={() => settle(true)}
              >
                {confirm.confirmLabel ?? "Confirm"}
              </button>
              <button className="toggle" onClick={() => settle(false)}>
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </UiContext.Provider>
  );
}

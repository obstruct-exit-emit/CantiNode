import { useCallback, useEffect, useState } from "react";
import {
  api,
  type BackupInfo,
  type HealthResult,
  type SystemSettings,
  type SystemStatus,
} from "../api";
import { formatBytes, relativeTime } from "../format";
import { useUi } from "../ui";

// waitForBackOnline polls systemStatus every intervalMs until it succeeds
// or maxWaitMs elapses, returning the fresh status (or null on timeout).
// Update/Restart are expected to make the server genuinely unreachable for
// a while — an immediate single follow-up check would almost always just
// find it still down — so every individual failure here just means "not
// back yet," never surfaced as an error on its own; only running out of
// time without ever succeeding is reported, by the caller.
async function waitForBackOnline(maxWaitMs: number, intervalMs: number): Promise<SystemStatus | null> {
  const deadline = Date.now() + maxWaitMs;
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
    try {
      return await api.systemStatus();
    } catch {
      // Not back yet — keep waiting rather than giving up on the first miss.
    }
  }
  return null;
}

// Module-level, not component state: SystemView unmounts the instant the
// admin navigates to another page (App.tsx only renders it while
// page.name === "system"), but an in-flight Update/Restart's 3-minute
// wait-for-back-online poll keeps running regardless. Found live: without
// this, navigating away and back resets the button to enabled on the fresh
// mount, letting the admin fire a second Update/Restart while the first is
// still genuinely in flight on the host — the same duplicate-request
// problem the backend's upgrade-claim compare-and-swap exists to prevent,
// just via navigation instead of a double-click. Surviving as a plain
// module variable (not sessionStorage) is enough — it only needs to
// outlive a SystemView remount, not a full page reload, and a page reload
// already drops the in-browser poll itself either way.
let systemActionBusy = false;
const systemActionListeners = new Set<(busy: boolean) => void>();
function setSystemActionBusy(busy: boolean) {
  systemActionBusy = busy;
  systemActionListeners.forEach((listen) => listen(busy));
}
function useSystemActionBusy(): boolean {
  const [busy, setBusy] = useState(systemActionBusy);
  useEffect(() => {
    systemActionListeners.add(setBusy);
    return () => {
      systemActionListeners.delete(setBusy);
    };
  }, []);
  return busy;
}

export default function SystemView({
  onError,
}: {
  onError: (message: string) => void;
}) {
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const { confirmDlg, toast } = useUi();
  const systemBusy = useSystemActionBusy();

  useEffect(() => {
    api
      .systemStatus()
      .then(setStatus)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  const runSystemAction = async (
    label: string,
    message: string,
    confirmLabel: string,
    action: () => Promise<{ status: string }>,
  ) => {
    if (!(await confirmDlg({ title: label, message, confirmLabel, danger: true }))) return;
    setSystemActionBusy(true);
    try {
      await action();
      toast(`${label} started — waiting for CantiNode to come back online…`, "info");
      // The whole point of either command is to stop or replace this
      // process, so it's expected to actually be unreachable for a while —
      // found live: a real update took well over two minutes, not the
      // handful of seconds a first guess might assume. Keep checking
      // instead of leaving the admin to guess whether it's still working
      // or has gotten stuck; every individual failed check here just means
      // "not back yet", not a real error.
      const back = await waitForBackOnline(3 * 60_000, 3_000);
      if (back) {
        setStatus(back);
        toast(`✓ ${label} finished — back online (version ${back.appVersion}).`, "ok");
      } else {
        toast(
          `⚠ Still unreachable 3 minutes after ${label.toLowerCase()} — check the host directly, CantiNode can't report on itself while it's down.`,
          "bad",
        );
      }
    } catch (err) {
      onError(String(err instanceof Error ? err.message : err));
    } finally {
      setSystemActionBusy(false);
    }
  };

  if (!status) return <p className="muted">Loading…</p>;

  return (
    <>
      <section className="card">
        <div className="card-head">
          <h2>System</h2>
          <span className="row-actions">
            <button
              disabled={systemBusy}
              onClick={() =>
                runSystemAction(
                  "Update",
                  "Run the update command and apply it? CantiNode will be unavailable until it restarts — this can take a few minutes, not just a few seconds.",
                  "Update",
                  api.systemUpdate,
                )
              }
            >
              Update
            </button>
            <button
              className="danger"
              disabled={systemBusy}
              onClick={() =>
                runSystemAction(
                  "Restart",
                  "Reboot the host machine now? Everything it runs will be unavailable until it comes back up, not just CantiNode.",
                  "Reboot now",
                  api.systemRestart,
                )
              }
            >
              Restart
            </button>
          </span>
        </div>
        <dl className="status-grid">
          <dt>Version</dt>
          <dd>{status.appVersion}</dd>
          <dt>Platform</dt>
          <dd>
            {status.os}/{status.arch}
          </dd>
          <dt>Uptime</dt>
          <dd>{status.uptime}</dd>
          <dt>Started</dt>
          <dd>{status.startTime}</dd>
          <dt>IP address{(status.ipAddresses ?? []).length === 1 ? "" : "es"}</dt>
          <dd>
            {(status.ipAddresses ?? []).length === 0
              ? "—"
              : status.ipAddresses.map((ip) => (
                  <div key={ip}>
                    <code>
                      http://{ip}:{status.port}
                    </code>
                  </div>
                ))}
          </dd>
          <dt>Data directory</dt>
          <dd>
            <code>{status.dataDir}</code>
          </dd>
        </dl>
      </section>
      <SystemCommandsCard onError={onError} />
      <HealthCard onError={onError} />
      <BackupsCard onError={onError} />
      <LogCard onError={onError} />
    </>
  );
}

// SystemCommandsCard lets an admin override the exact host commands the
// Update/Restart buttons above run — see config.SystemSettings' own doc
// comment for why this needs to be a real setting rather than fixed in
// source: what "update" or "restart" means is entirely host/deployment
// specific. A blank field keeps the built-in default (shown as its own
// placeholder), not an empty command.
function SystemCommandsCard({ onError }: { onError: (message: string) => void }) {
  const [settings, setSettings] = useState<SystemSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .getSystemSettings()
      .then(setSettings)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!settings) return null;

  const save = () => {
    setBusy(true);
    setNotice("");
    api
      .saveSystemSettings(settings)
      .then((saved) => {
        setSettings(saved);
        setNotice("✓ Saved");
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  return (
    <section className="card">
      <div className="card-head">
        <h2>Update &amp; restart commands</h2>
        <button disabled={busy} onClick={save}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
      <p className="muted">
        The exact host commands the Update/Restart buttons above run, via a
        login shell (<code>bash -lc</code>). Leave a field blank to use the
        default shown as its placeholder.
      </p>
      <label>
        Update command
        <input
          type="text"
          placeholder="update"
          value={settings.updateCommand}
          onChange={(e) => setSettings({ ...settings, updateCommand: e.target.value })}
        />
      </label>
      <label>
        Restart command
        <input
          type="text"
          placeholder="reboot now"
          value={settings.restartCommand}
          onChange={(e) => setSettings({ ...settings, restartCommand: e.target.value })}
        />
      </label>
      {notice && <p className="notice ok">{notice}</p>}
    </section>
  );
}

// BackupsCard manages zip backups of the database + config: create, download,
// delete, and stage a restore (applied on the next server start).
function BackupsCard({ onError }: { onError: (message: string) => void }) {
  const { confirmDlg } = useUi();
  const [backups, setBackups] = useState<BackupInfo[]>([]);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .listBackups()
      .then(setBackups)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  const run = (action: () => Promise<unknown>, done?: string) => {
    setBusy(true);
    setNotice("");
    action()
      .then(() => {
        if (done) setNotice(done);
        reload();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const download = (b: BackupInfo) => {
    setBusy(true);
    api
      .downloadBackup(b.name)
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = b.name;
        a.click();
        URL.revokeObjectURL(url);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  return (
    <section className="card">
      <div className="card-head">
        <h2>Backups</h2>
        <button
          disabled={busy}
          onClick={() => run(() => api.createBackup(), "✓ Backup created")}
        >
          Backup now
        </button>
      </div>
      <p className="muted">
        A backup is a zip of the database (consistent snapshot) and{" "}
        <code>config.yaml</code>, stored under <code>backups/</code> in the
        data directory. Restoring stages the files and applies them on the
        next server start — the replaced files are kept as{" "}
        <code>*.pre-restore</code>.
      </p>
      {notice && (
        <p className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</p>
      )}
      {backups.length === 0 ? (
        <p className="muted">No backups yet.</p>
      ) : (
        <ul className="rows">
          {backups.map((b) => (
            <li key={b.name}>
              <div className="row">
                <span className="file-path">{b.name}</span>
                <span className="row-actions">
                  <span className="muted" title={b.createdAt}>
                    {relativeTime(b.createdAt)}
                  </span>
                  <span className="muted">{formatBytes(b.size) || "—"}</span>
                  <button disabled={busy} onClick={() => download(b)}>
                    Download
                  </button>
                  <button
                    disabled={busy}
                    onClick={async () => {
                      if (
                        await confirmDlg({
                          title: "Restore backup",
                          message: `Restore ${b.name}?\n\nThe current database and config are replaced on the next restart (kept as *.pre-restore).`,
                          confirmLabel: "Stage restore",
                          danger: true,
                        })
                      ) {
                        run(() => api.restoreBackup(b.name), "✓ Restore staged — restart CantiNode to apply");
                      }
                    }}
                  >
                    Restore
                  </button>
                  <button
                    className="danger"
                    disabled={busy}
                    onClick={async () => {
                      if (
                        await confirmDlg({
                          message: `Delete backup ${b.name}?`,
                          confirmLabel: "Delete",
                          danger: true,
                        })
                      ) {
                        run(() => api.deleteBackup(b.name));
                      }
                    }}
                  >
                    delete
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

// LogCard tails the on-disk log file (System → events): pick how many lines,
// filter by text (e.g. "ERROR" or a book title), refresh on demand.
function LogCard({ onError }: { onError: (message: string) => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const [count, setCount] = useState(200);
  const [filter, setFilter] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    setBusy(true);
    api
      .logTail(count)
      .then((r) => setLines(r.lines))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  }, [count, onError]);

  useEffect(reload, [reload]);

  const shown = filter
    ? lines.filter((l) => l.toLowerCase().includes(filter.toLowerCase()))
    : lines;

  return (
    <section className="card">
      <div className="card-head">
        <h2>Log</h2>
        <span className="row-actions">
          <select value={count} onChange={(e) => setCount(Number(e.target.value))}>
            <option value={100}>100 lines</option>
            <option value={200}>200 lines</option>
            <option value={500}>500 lines</option>
            <option value={2000}>2000 lines</option>
          </select>
          <input
            placeholder="Filter (e.g. ERROR)"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <button disabled={busy} onClick={reload}>
            {busy ? "Loading…" : "Refresh"}
          </button>
        </span>
      </div>
      {shown.length === 0 ? (
        <p className="muted">
          {lines.length === 0
            ? "No log entries yet — the file starts with the next server start after updating."
            : "No lines match the filter."}
        </p>
      ) : (
        <pre className="log-view">
          {shown.map((l, i) => (
            <div
              key={i}
              className={
                l.includes("level=ERROR")
                  ? "log-line err"
                  : l.includes("level=WARN")
                    ? "log-line warn"
                    : "log-line"
              }
            >
              {l}
            </div>
          ))}
        </pre>
      )}
    </section>
  );
}

// HealthCard shows the latest background check results and can re-run them
// on demand (checks cover root folders, indexers, download clients, and the
// metadata provider token).
function HealthCard({ onError }: { onError: (message: string) => void }) {
  const [result, setResult] = useState<HealthResult | null>(null);
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    api
      .health()
      .then(setResult)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  const runNow = () => {
    setBusy(true);
    api
      .checkHealth()
      .then(setResult)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  const hasRun = result && !result.checkedAt.startsWith("0001-");

  return (
    <section className="card">
      <div className="card-head">
        <h2>Health</h2>
        <button disabled={busy} onClick={runNow} title="Re-run every check now">
          {busy ? "Checking…" : "Run checks now"}
        </button>
      </div>
      <p className="muted">
        Root folders, indexers, download clients, and the metadata provider
        are checked in the background every 15 minutes.
        {hasRun && ` Last run: ${new Date(result.checkedAt).toLocaleString()}.`}
      </p>
      {!hasRun ? (
        <p className="muted">No check has completed yet.</p>
      ) : result.issues.length === 0 ? (
        <p className="notice ok">✓ All checks passed</p>
      ) : (
        <ul className="rows">
          {result.issues.map((issue, i) => (
            <li key={i}>
              <p className={issue.level === "error" ? "health-issue error" : "health-issue"}>
                {issue.level === "error" ? "⛔" : "⚠️"} {issue.message}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

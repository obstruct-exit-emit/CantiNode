import { type ReactNode, useCallback, useEffect, useState } from "react";
import FolderBrowser from "../components/FolderBrowser";
import { formatBytes } from "../format";
import {
  api,
  getApiKey,
  setApiKey,
  type AuthStatus,
  type DownloadClient,
  type Indexer,
  type MusicSettings,
  type NamingSettings,
  type NativeIndexer,
  type PathMapping,
  type QualityProfile,
  type RootFolder,
  type SystemStatus,
  type TimingSettings,
  type UserAccount,
} from "../api";
import { useUi } from "../ui";

// Settings groups, *arr-style: pages organized by concern instead of one
// long scroll. Each carries an icon and a one-line blurb so the section a
// user lands on always explains itself. Order matches the README spec.
const settingsGroups = [
  { name: "Media Management", icon: "📁", blurb: "Where your library lives on disk, and how organized files are named." },
  { name: "Music", icon: "🎵", blurb: "MusicBrainz/TheAudioDB matching behavior, and the image cache." },
  { name: "Quality Profiles", icon: "⭐", blurb: "Which release formats are acceptable and preferred." },
  { name: "Indexers", icon: "🔎", blurb: "Newznab and Torznab search sources — added by hand or synced from Prowlarr." },
  { name: "Download Clients", icon: "⬇️", blurb: "Where grabbed releases are sent, and how finished downloads are handled." },
  { name: "General", icon: "⚙️", blurb: "Login accounts, the API key, and this instance's details." },
] as const;
type SettingsGroup = (typeof settingsGroups)[number]["name"];

// --- Shared layout primitives (visual polish; no behavior of their own) ---

// Section groups related fields inside a card under a small heading, with
// optional help text — so a long form reads as a few labelled blocks.
function Section({
  title,
  help,
  children,
}: {
  title: string;
  help?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="settings-section">
      <h3>{title}</h3>
      {help != null && <p className="muted">{help}</p>}
      {children}
    </div>
  );
}

// Disclosure hides advanced/optional fields behind a native <details> toggle,
// collapsed by default, so the common path stays uncluttered.
function Disclosure({
  summary,
  defaultOpen,
  children,
}: {
  summary: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  return (
    <details className="disclosure" open={defaultOpen}>
      <summary>{summary}</summary>
      <div className="disclosure-body">{children}</div>
    </details>
  );
}

export default function SettingsView({
  isAdmin,
  onError,
  onLibrariesChanged,
}: {
  isAdmin: boolean;
  onError: (message: string) => void;
  onLibrariesChanged?: () => void;
}) {
  const [group, setGroup] = useState<SettingsGroup>("Media Management");

  // Plex-style gating: music-specific settings render only once a music
  // root folder is set up. Root Folders itself always offers it — that's
  // how the library gets created in the first place.
  const [activeTypes, setActiveTypes] = useState<string[]>([]);
  const reloadLibraries = useCallback(() => {
    api
      .listRootFolders()
      .then((folders) => setActiveTypes(folders.some((f) => f.mediaType === "music") ? ["music"] : []))
      .catch(() => setActiveTypes([]));
  }, []);
  useEffect(reloadLibraries, [reloadLibraries]);

  const librariesChanged = () => {
    reloadLibraries();
    onLibrariesChanged?.();
  };

  // Members get no server-configuration UI at all (every other card's
  // backing endpoints are admin-only and would just 403) — only the
  // self-service password change inside Security.
  if (!isAdmin) {
    return (
      <>
        <header className="settings-header">
          <h1>Settings</h1>
          <p className="muted">Manage your account.</p>
        </header>
        <SecurityCard onError={onError} isAdmin={false} />
      </>
    );
  }

  const current = settingsGroups.find((g) => g.name === group) ?? settingsGroups[0];

  return (
    <>
      <header className="settings-header">
        <h1>Settings</h1>
        <p className="muted">{current.blurb}</p>
      </header>

      <nav className="subnav" aria-label="Settings sections">
        {settingsGroups.map((g) => (
          <button
            key={g.name}
            className={g.name === group ? "tab active" : "tab"}
            aria-current={g.name === group ? "page" : undefined}
            onClick={() => setGroup(g.name)}
          >
            <span className="tab-icon" aria-hidden="true">{g.icon}</span> {g.name}
          </button>
        ))}
      </nav>

      {group === "Media Management" && (
        <>
          <RootFoldersCard onError={onError} onChanged={librariesChanged} />
          <NamingCard onError={onError} />
        </>
      )}
      {group === "Music" && <MusicCard onError={onError} />}
      {group === "Quality Profiles" && (
        <QualityProfilesCard onError={onError} activeTypes={activeTypes} />
      )}
      {group === "Indexers" && <IndexersCard onError={onError} />}
      {group === "Download Clients" && (
        <>
          <DownloadClientsCard onError={onError} />
          <PathMappingsPanel onError={onError} />
        </>
      )}
      {group === "General" && <GeneralCard onError={onError} />}
    </>
  );
}

// General: instance facts, the login account, and the API key. Server-side
// options (host/port, SSL, proxy) live in config.yaml — see the README's
// reverse-proxy section for HTTPS guidance.
function GeneralCard({ onError }: { onError: (message: string) => void }) {
  const { confirmDlg } = useUi();
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [key, setKey] = useState(getApiKey());
  const [showKey, setShowKey] = useState(false);
  const [keyNotice, setKeyNotice] = useState("");

  useEffect(() => {
    api
      .systemStatus()
      .then(setStatus)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  const regenerate = async () => {
    const ok = await confirmDlg({
      title: "Regenerate API key",
      message:
        "Regenerate the API key?\n\nProwlarr and any scripts using the current key stop working until you update them.",
      confirmLabel: "Regenerate",
      danger: true,
    });
    if (!ok) return;
    api
      .regenerateApiKey()
      .then((r) => {
        setApiKey(r.apiKey); // keep this browser working
        setKey(r.apiKey);
        setKeyNotice("✓ New key generated — update Prowlarr and any scripts using the old one");
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  return (
    <>
      <SecurityCard onError={onError} isAdmin />

      <section className="card">
        <h2>API Key</h2>
        <p className="muted">
          Used by scripts and Prowlarr (and by this browser when no login
          account is set). Regenerating invalidates the old key everywhere.
        </p>
        <div className="settings-form">
          <label>
            API key
            <span className="token-row">
              <input
                type={showKey ? "text" : "password"}
                value={key}
                onChange={(e) => setKey(e.target.value)}
              />
              <button type="button" className="toggle" onClick={() => setShowKey(!showKey)}>
                {showKey ? "hide" : "show"}
              </button>
            </span>
          </label>
          <div className="settings-actions">
            <button
              disabled={!key.trim() || key.trim() === getApiKey()}
              onClick={() => {
                setApiKey(key.trim());
                location.reload();
              }}
            >
              Save & reconnect
            </button>
            <button className="danger" onClick={regenerate}>
              Regenerate
            </button>
            {keyNotice && <span className="notice ok">{keyNotice}</span>}
          </div>
        </div>
      </section>

      <section className="card">
        <h2>Instance</h2>
        {status ? (
          <dl>
            <dt>Version</dt>
            <dd>{status.appVersion ?? status.version}</dd>
            <dt>Platform</dt>
            <dd>
              {status.os}/{status.arch}
            </dd>
            <dt>Data directory</dt>
            <dd>{status.dataDir}</dd>
            <dt>Uptime</dt>
            <dd>{status.uptime}</dd>
          </dl>
        ) : (
          <p className="muted">Loading…</p>
        )}
        <p className="muted" style={{ marginBottom: 0 }}>
          Host, port, and data directory are set in <code>config.yaml</code>{" "}
          (or <code>CANTINODE_*</code> environment variables) and need a
          restart. For HTTPS, run CantiNode behind a reverse proxy — see the
          README. Health checks, logs, and backups live on the System page.
        </p>
        <Disclosure summary="Advanced: background timings">
          <TimingsPanel onError={onError} />
        </Disclosure>
      </section>
    </>
  );
}

// TimingsPanel tunes the background loop cadences. Blank/0 = the built-in
// default; entered values are clamped server-side. Applied at startup.
function TimingsPanel({ onError }: { onError: (message: string) => void }) {
  const [timings, setTimings] = useState<TimingSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .getTimingSettings()
      .then(setTimings)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!timings) return <p className="muted">Loading…</p>;

  const field = (
    label: string,
    key: keyof TimingSettings,
    hint: string,
    range: string,
  ) => (
    <label>
      {label}
      <input
        type="number"
        placeholder={hint}
        title={`${hint}; allowed ${range}. Blank = default.`}
        value={timings[key] === 0 ? "" : timings[key]}
        onChange={(e) =>
          setTimings({ ...timings, [key]: Number(e.target.value) || 0 })
        }
      />
    </label>
  );

  const save = () => {
    setBusy(true);
    setNotice("");
    api
      .saveTimingSettings(timings)
      .then((saved) => {
        setTimings(saved);
        setNotice("✓ Saved — cadences apply after the next restart");
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <p className="muted">
        How often the background health check runs — the only loop on a
        schedule today. Search, scan, and organize are all triggered by you
        (from the artist page or Activity), not on a timer. Blank uses the
        default; out-of-range values are clamped. Changes apply on the next
        server start.
      </p>
      <div className="settings-form">
        {field("Health checks (minutes)", "healthIntervalMinutes", "default 15", "5–1440")}
      </div>
      <div className="settings-actions">
        <button disabled={busy} onClick={save}>
          {busy ? "Saving…" : "Save timings"}
        </button>
        {notice && <span className="notice ok">{notice}</span>}
      </div>
    </>
  );
}

// MusicCard tunes internal/musicscanner's MusicBrainz matching and
// TheAudioDB lookups, plus the cached provider-image store both (and Cover
// Art Archive) fill.
function MusicCard({ onError }: { onError: (message: string) => void }) {
  const [settings, setSettings] = useState<MusicSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .getMusicSettings()
      .then(setSettings)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!settings) return <p className="muted">Loading…</p>;

  const save = () => {
    setBusy(true);
    setNotice("");
    api
      .saveMusicSettings(settings)
      .then((s) => {
        setSettings(s);
        setNotice("✓ Saved");
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  return (
    <section className="card">
      <h2>Music Matching</h2>
      <p className="muted">
        How CantiNode talks to MusicBrainz and TheAudioDB, and how confident
        a scan's fuzzy match has to be before it's accepted automatically.
      </p>
      <div className="settings-form">
        <Section
          title="MusicBrainz"
          help={
            <>
              Included in the User-Agent CantiNode sends MusicBrainz, per
              their{" "}
              <a
                href="https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting"
                target="_blank"
                rel="noreferrer"
              >
                API usage policy
              </a>
              , so they can reach you instead of just blocking a misbehaving
              instance. Optional, but recommended.
            </>
          }
        >
          <label>
            Contact email
            <input
              type="email"
              placeholder="you@example.com"
              value={settings.musicbrainzContactEmail}
              onChange={(e) =>
                setSettings({ ...settings, musicbrainzContactEmail: e.target.value })
              }
            />
          </label>
        </Section>

        <Section
          title="TheAudioDB"
          help="Powers artist bio/photo lookups. Optional — an empty key falls back to TheAudioDB's shared public test key, which is fine for light use but can rate-limit under heavier use. A free key removes that limit."
        >
          <label>
            API key
            <input
              type="text"
              placeholder="(using the shared public test key)"
              value={settings.audioDbApiKey}
              onChange={(e) => setSettings({ ...settings, audioDbApiKey: e.target.value })}
            />
          </label>
        </Section>

        <Section title="Matching">
          <label>
            <span>
              <input
                type="checkbox"
                checked={settings.organizeOnMatch}
                onChange={(e) =>
                  setSettings({ ...settings, organizeOnMatch: e.target.checked })
                }
              />{" "}
              Organize a file immediately once a scan matches it
            </span>
          </label>
          <p className="muted field-note">
            Off by default: a first scan of an existing library can match
            hundreds of files at once, and moving files on disk is much
            harder to casually undo than a database row. Leave this off to
            review a scan's matches, then run Organize yourself when ready.
          </p>
          <label>
            Minimum match confidence:{" "}
            {Math.round(settings.minMatchConfidence * 100)}%
            <input
              type="range"
              min={0.5}
              max={1}
              step={0.01}
              value={settings.minMatchConfidence}
              onChange={(e) =>
                setSettings({ ...settings, minMatchConfidence: Number(e.target.value) })
              }
            />
          </label>
          <p className="muted field-note">
            How sure a fuzzy MusicBrainz title search has to be before a scan
            accepts it automatically — anything below is left unmatched for
            manual review instead. Has no effect on a direct match from a
            file's own embedded tags or a whole-folder release match; both
            are always accepted. Default 75%.
          </p>
        </Section>

        <div className="settings-actions">
          <button disabled={busy} onClick={save}>
            {busy ? "Saving…" : "Save"}
          </button>
          {notice && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
              {notice}
            </span>
          )}
        </div>
      </div>

      <ImageCacheSection />
    </section>
  );
}

// ImageCacheSection clears the cached artist/album provider art (TheAudioDB
// bios/photos, Cover Art Archive covers) — rebuilds on demand, so this is
// always safe; useful after a lot of churn or to reclaim disk space.
function ImageCacheSection() {
  const { confirmDlg } = useUi();
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const clear = async () => {
    const ok = await confirmDlg({
      title: "Clear image cache",
      message:
        "Clear the cached artist photos and album art? They're re-downloaded from the provider the next time they're needed.",
      confirmLabel: "Clear cache",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    setNotice("");
    api
      .clearAllCache()
      .then((r) =>
        setNotice(`✓ Cleared ${r.removed} file(s), ${formatBytes(r.freedBytes)} freed`),
      )
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  return (
    <Section
      title="Image cache"
      help="Artist photos and album covers, downloaded once and served locally so the UI never re-fetches them just from browsing. Safe to clear — it rebuilds on demand."
    >
      <div className="settings-actions">
        <button className="danger" disabled={busy} onClick={clear}>
          Clear image cache
        </button>
        {notice && (
          <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
            {notice}
          </span>
        )}
      </div>
    </Section>
  );
}

// SecurityCard manages the login accounts: a compact user list with per-user
// actions (change password, make default, remove) plus add-user and
// disable-login. The default user is protected — promote another user first.
// The API key keeps working for scripts and Prowlarr either way.
function SecurityCard({
  onError,
  isAdmin,
}: {
  onError: (message: string) => void;
  isAdmin: boolean;
}) {
  const { confirmDlg } = useUi();
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [users, setUsers] = useState<UserAccount[]>([]);
  // One inline form open at a time: adding a user, or changing one password.
  const [form, setForm] = useState<"" | "add" | `pw:${string}`>("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [role, setRole] = useState<"admin" | "member">("member");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .authStatus()
      .then((s) => {
        setStatus(s);
        return s.authEnabled && isAdmin ? api.listUsers().then((r) => setUsers(r.users)) : setUsers([]);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError, isAdmin]);

  useEffect(reload, [reload]);

  const openForm = (f: "" | "add" | `pw:${string}`) => {
    setForm(f);
    setUsername("");
    setPassword("");
    setConfirmPw("");
    setRole("member");
    setNotice("");
  };

  const run = (action: () => Promise<unknown>, done?: string) => {
    setBusy(true);
    setNotice("");
    action()
      .then(() => {
        openForm("");
        if (done) setNotice(done); // after openForm — it clears the notice
        reload();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const passwordsOK = password.length >= 8 && password === confirmPw;
  const pwHint =
    password && password.length < 8
      ? "min. 8 characters"
      : confirmPw && password !== confirmPw
        ? "passwords don't match"
        : "";

  const submitForm = () => {
    if (form === "add") {
      // The very first account goes through the credentials endpoint, which
      // also signs this browser in before the login requirement kicks in.
      run(
        () =>
          users.length === 0
            ? api.setCredentials(username.trim(), password)
            : api.addUser(username.trim(), password, role),
        users.length === 0
          ? "✓ Login required from now on — this browser is already signed in"
          : `✓ Added ${username.trim()}`,
      );
    } else if (form.startsWith("pw:")) {
      const user = form.slice(3);
      run(() => api.setUserPassword(user, password), `✓ Password changed for ${user}`);
    }
  };

  const remove = async (u: UserAccount) => {
    const ok = await confirmDlg({
      title: "Remove user",
      message: `Remove user "${u.username}"? Their sessions keep working until the next restart.`,
      confirmLabel: "Remove user",
      danger: true,
    });
    if (ok) run(() => api.removeUser(u.username), `✓ Removed ${u.username}`);
  };

  const disable = async () => {
    const ok = await confirmDlg({
      title: "Disable login",
      message:
        "Disable the login requirement? All users are removed and the UI goes back to the API-key prompt.",
      confirmLabel: "Disable login",
      danger: true,
    });
    if (ok) run(() => api.setCredentials("", ""), "✓ Login disabled");
  };

  // The inline username/password form (add user or change password).
  const credentialForm = (title: string, withUsername: boolean, withRole?: boolean) => (
    <div className="settings-form user-form">
      {withUsername && (
        <label>
          Username
          <input autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
        </label>
      )}
      {withRole && (
        <label>
          Role
          <select value={role} onChange={(e) => setRole(e.target.value as "admin" | "member")}>
            <option value="member">Member — everyday use, no server configuration</option>
            <option value="admin">Admin — full access, including settings and accounts</option>
          </select>
        </label>
      )}
      <label>
        Password
        <input
          type="password"
          autoFocus={!withUsername}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      <label>
        Confirm password
        <input type="password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
      </label>
      <div className="settings-actions">
        <button
          disabled={busy || (withUsername && !username.trim()) || !passwordsOK}
          onClick={submitForm}
        >
          {title}
        </button>
        <button className="toggle" onClick={() => openForm("")}>
          Cancel
        </button>
        {pwHint && <span className="muted">{pwHint}</span>}
      </div>
    </div>
  );

  if (!isAdmin) {
    return (
      <section className="card">
        <h2>Security</h2>
        <p className="muted">
          Signed in as <strong>{status?.username}</strong> ({status?.role}).
          Account management — adding users, roles, removal — is admin-only;
          ask an admin for that.
        </p>
        {form === `pw:${status?.username}` ? (
          credentialForm("Change password", false)
        ) : (
          <div className="settings-actions">
            <button disabled={busy || !status?.username} onClick={() => openForm(`pw:${status?.username}`)}>
              Change my password
            </button>
          </div>
        )}
        {notice && (
          <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</span>
        )}
      </section>
    );
  }

  return (
    <section className="card">
      <h2>Security</h2>
      <p className="muted">
        {status?.authEnabled
          ? "Signing in is required. The default user is protected — make another user the default before removing it. The API key keeps working for Prowlarr and scripts."
          : "No login account yet — the UI asks for the raw API key. Add a user to switch to a login page (sessions last 30 days; a restart signs everyone out)."}
      </p>

      {users.length > 0 && (
        <ul className="rows">
          {users.map((u) => (
            <li key={u.username}>
              <div className="row">
                <span>
                  👤 {u.username}
                  <span
                    className="pill"
                    title={u.role === "admin" ? "Full access, including settings" : "No server configuration access"}
                  >
                    {u.role}
                  </span>
                  {u.default && (
                    <span className="pill user-default" title="Protected — cannot be removed">
                      default
                    </span>
                  )}
                </span>
                <span className="row-actions">
                  <button
                    className="toggle"
                    disabled={busy}
                    onClick={() => openForm(`pw:${u.username}`)}
                  >
                    change password
                  </button>
                  {!u.default && (
                    <>
                      <button
                        className="toggle"
                        disabled={busy}
                        title={
                          u.role === "admin"
                            ? "Demote — loses access to settings and accounts"
                            : "Promote — full access, including settings and accounts"
                        }
                        onClick={() =>
                          run(
                            () => api.setUserRole(u.username, u.role === "admin" ? "member" : "admin"),
                            `✓ ${u.username} is now ${u.role === "admin" ? "a member" : "an admin"}`,
                          )
                        }
                      >
                        {u.role === "admin" ? "demote to member" : "promote to admin"}
                      </button>
                      <button
                        className="toggle"
                        disabled={busy}
                        title="Make this the protected primary account"
                        onClick={() => run(() => api.makeDefaultUser(u.username), `✓ ${u.username} is now the default`)}
                      >
                        make default
                      </button>
                      <button className="danger" disabled={busy} onClick={() => remove(u)}>
                        remove
                      </button>
                    </>
                  )}
                </span>
              </div>
              {form === `pw:${u.username}` && credentialForm("Change password", false)}
            </li>
          ))}
        </ul>
      )}

      {form === "add" &&
        credentialForm(users.length === 0 ? "Enable login" : "Add user", true, users.length > 0)}

      <div className="settings-actions" style={{ marginTop: "0.6rem" }}>
        {form !== "add" && (
          <button disabled={busy} onClick={() => openForm("add")}>
            + Add user
          </button>
        )}
        {status?.authEnabled && (
          <button className="danger" disabled={busy} onClick={disable}>
            Disable login
          </button>
        )}
        {notice && (
          <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</span>
        )}
      </div>
    </section>
  );
}

const emptyDownloadClient: Omit<DownloadClient, "id"> = {
  name: "",
  type: "qbittorrent",
  host: "",
  username: "",
  password: "",
  apiKey: "",
  category: "cantinode",
  enabled: true,
  priority: 1,
};

function DownloadClientsCard({
  onError,
}: {
  onError: (message: string) => void;
}) {
  const { confirmDlg } = useUi();
  const [clients, setClients] = useState<DownloadClient[]>([]);
  const [draft, setDraft] = useState(emptyDownloadClient);
  // Edit-in-place: the saved client loaded into the form, or null when adding.
  const [editing, setEditing] = useState<DownloadClient | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .listDownloadClients()
      .then(setClients)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  const set = (patch: Partial<typeof emptyDownloadClient>) =>
    setDraft((d) => ({ ...d, ...patch }));

  const act = (action: () => Promise<unknown>, done?: string) => {
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

  // The SABnzbd API key is optional — SABnzbd-compatible endpoints like
  // Real-Debrid's need no key (real SABnzbd rejects unauthenticated calls,
  // which the Test button surfaces). The direct fetcher's "host" is a local
  // download folder, not a URL.
  const draftValid =
    draft.name.trim() !== "" &&
    (draft.type === "direct"
      ? draft.host.trim() !== ""
      : /^https?:\/\//.test(draft.host.trim()));

  const startEdit = (c: DownloadClient) => {
    setEditing(c);
    setDraft({ ...c });
    setNotice("");
  };

  const cancelEdit = () => {
    setEditing(null);
    setDraft(emptyDownloadClient);
    setNotice("");
  };

  const saveOrAdd = () => {
    if (editing) {
      act(
        () =>
          api.updateDownloadClient({ ...editing, ...draft }).then(() => {
            setEditing(null);
            setDraft(emptyDownloadClient);
          }),
        `✓ ${draft.name} saved`,
      );
    } else {
      act(() => api.addDownloadClient(draft).then(() => setDraft(emptyDownloadClient)), "✓ Client added");
    }
  };

  return (
    <section className="card">
      <h2>Download Clients</h2>
      <p className="muted">
        Where grabbed releases go: <strong>qBittorrent</strong> for torrents,{" "}
        <strong>SABnzbd</strong> for usenet. Downloads are tagged with the
        category so CantiNode only tracks its own.
      </p>

      {clients.length > 0 && (
        <ul className="rows">
          {clients.map((c) => (
            <li key={c.id}>
              <div className="row">
                <span className="saved-main">
                  <span className="saved-head">
                    <strong>{c.name}</strong>
                    <span className="pill" title={c.type}>
                      {c.type === "qbittorrent"
                        ? "🧲 qBittorrent"
                        : c.type === "direct"
                          ? "⬇️ Direct fetcher"
                          : "📡 SABnzbd"}
                    </span>
                    <span className="pill" title="Priority — lower wins ties">
                      prio {c.priority}
                    </span>
                    {!c.enabled && <span className="pill off">disabled</span>}
                  </span>
                  <span className="muted file-path saved-sub">{c.host}</span>
                </span>
                <span className="row-actions">
                  <button
                    className="toggle"
                    disabled={busy}
                    title="Check the saved connection still works"
                    onClick={() => act(async () => {
                      await api.testDownloadClient(c);
                    }, `✓ ${c.name}: connection OK`)}
                  >
                    test
                  </button>
                  <button
                    className={c.enabled ? "toggle on" : "toggle"}
                    disabled={busy}
                    onClick={() => act(() => api.updateDownloadClient({ ...c, enabled: !c.enabled }))}
                  >
                    {c.enabled ? "enabled" : "disabled"}
                  </button>
                  <button
                    className={editing?.id === c.id ? "toggle on" : "toggle"}
                    disabled={busy}
                    title="Load this client into the form below to change its host, credentials, or priority"
                    onClick={() => (editing?.id === c.id ? cancelEdit() : startEdit(c))}
                  >
                    edit
                  </button>
                  <button
                    className="danger"
                    disabled={busy}
                    onClick={async () => {
                      if (
                        await confirmDlg({
                          message: `Remove download client ${c.name}?`,
                          confirmLabel: "Remove",
                          danger: true,
                        })
                      ) {
                        act(() => api.deleteDownloadClient(c.id));
                      }
                    }}
                  >
                    remove
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}

      <h3 className="settings-subhead">
        {editing
          ? `Edit ${editing.name}`
          : clients.length > 0
            ? "Add another client"
            : "Add a download client"}
      </h3>
      <div className="settings-form">
        <label>
          Name
          <input value={draft.name} onChange={(e) => set({ name: e.target.value })} />
        </label>
        <label>
          Type
          <select
            value={draft.type}
            onChange={(e) => set({ type: e.target.value as DownloadClient["type"] })}
          >
            <option value="qbittorrent">qBittorrent (torrents)</option>
            <option value="sabnzbd">SABnzbd (usenet)</option>
            <option value="direct">Direct fetcher (built-in — plain HTTP downloads) ⚠ WIP</option>
          </select>
        </label>
        {draft.type === "direct" ? (
          <>
            <p className="notice bad field-note">
              ⚠ <strong>Work in progress.</strong> The direct HTTP fetcher pairs
              with the experimental shadow-library sources and is still wonky —
              mirror hand-offs and landing pages don&apos;t all resolve yet.
              Expect failures.
            </p>
            <p className="muted field-note">
              CantiNode downloads the file itself — no external program. Needed
              only for <em>direct</em>-protocol sources (e.g. Anna&apos;s
              Archive); torrents and usenet keep using the clients above.
            </p>
            <label>
              Download folder (on this server; finished files import from here)
              <input
                placeholder="/downloads/cantinode"
                value={draft.host}
                onChange={(e) => set({ host: e.target.value })}
              />
            </label>
          </>
        ) : (
          <label>
            Host
            <input
              placeholder="http://localhost:8080"
              value={draft.host}
              onChange={(e) => set({ host: e.target.value })}
            />
          </label>
        )}
        {draft.type === "qbittorrent" && (
          <>
            <label>
              Username
              <input
                value={draft.username}
                onChange={(e) => set({ username: e.target.value })}
              />
            </label>
            <label>
              Password
              <input
                type="password"
                value={draft.password}
                onChange={(e) => set({ password: e.target.value })}
              />
            </label>
          </>
        )}
        {draft.type === "sabnzbd" && (
          <label>
            API key
            <input
              placeholder="Optional — leave blank for Real-Debrid / keyless SABnzbd endpoints"
              value={draft.apiKey}
              onChange={(e) => set({ apiKey: e.target.value })}
            />
          </label>
        )}
        <Disclosure summary="Advanced">
          <label>
            Category
            <input
              value={draft.category}
              onChange={(e) => set({ category: e.target.value })}
            />
          </label>
          <p className="muted field-note">
            Downloads are tagged with this category so CantiNode only tracks its
            own — change it only if it collides with another app on the same
            client.
          </p>
          <label>
            Priority (1–50, lower wins ties)
            <input
              type="number"
              min={1}
              max={50}
              value={draft.priority}
              onChange={(e) => set({ priority: Number(e.target.value) || 1 })}
            />
          </label>
        </Disclosure>
        <div className="settings-actions">
          <button
            disabled={busy || !draftValid}
            onClick={() => {
              setBusy(true);
              setNotice("");
              api
                .testDownloadClient(draft)
                .then(() => setNotice("✓ Connection OK"))
                .catch((err: unknown) =>
                  setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
                )
                .finally(() => setBusy(false));
            }}
          >
            Test
          </button>
          <button disabled={busy || !draftValid} onClick={saveOrAdd}>
            {editing ? "Save changes" : "Add"}
          </button>
          {editing && (
            <button className="toggle" disabled={busy} onClick={cancelEdit}>
              Cancel
            </button>
          )}
          {notice && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
              {notice}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}

// PathMappingsPanel: remote→local path mappings for clients that run on
// another machine or in a container and report their own filesystem. The
// importer translates every client-reported path through these before it
// touches disk. Longest matching prefix wins.
function PathMappingsPanel({ onError }: { onError: (message: string) => void }) {
  const [mappings, setMappings] = useState<PathMapping[] | null>(null);
  const [remote, setRemote] = useState("");
  const [local, setLocal] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .getPathMappings()
      .then(setMappings)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!mappings) return null;

  const save = (next: PathMapping[], done: string) => {
    setBusy(true);
    setNotice("");
    api
      .savePathMappings(next)
      .then((saved) => {
        setMappings(saved);
        setNotice(done);
        setRemote("");
        setLocal("");
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setBusy(false));
  };

  return (
    <section className="card">
      <h2>Remote path mappings</h2>
      <p className="muted">
        For download clients on another machine or in a container: when the
        client reports <code>/storage_1/…</code> but this server sees the same
        files at <code>/mnt/media/…</code>, map the prefix here instead of
        having to mount the share at the exact same path. Applied to every
        completed download before import; the longest matching prefix wins.
      </p>
      {notice && <p className="notice ok">{notice}</p>}
      {mappings.length > 0 && (
        <ul className="rows">
          {mappings.map((m, i) => (
            <li key={`${m.remotePrefix}→${m.localPrefix}`}>
              <div className="row">
                <span className="file-path">
                  <code>{m.remotePrefix}</code> → <code>{m.localPrefix}</code>
                </span>
                <span className="row-actions">
                  <button
                    className="toggle"
                    disabled={busy}
                    title="Remove this mapping"
                    onClick={() => save(mappings.filter((_, j) => j !== i), "✓ Mapping removed")}
                  >
                    remove
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="settings-form">
        <label>
          Remote path (as the client reports it)
          <input
            placeholder="/storage_1"
            value={remote}
            onChange={(e) => setRemote(e.target.value)}
          />
        </label>
        <label>
          Local path (where this server sees those files)
          <input
            placeholder="/mnt/media"
            value={local}
            onChange={(e) => setLocal(e.target.value)}
          />
        </label>
      </div>
      <div className="settings-actions">
        <button
          disabled={busy || !remote.trim() || !local.trim()}
          onClick={() =>
            save(
              [...mappings, { remotePrefix: remote.trim(), localPrefix: local.trim() }],
              "✓ Mapping added — applies from the next scan",
            )
          }
        >
          + Add mapping
        </button>
      </div>
    </section>
  );
}

// Formats known to be used — offered as suggestions in the chips editor
// (anything else can still be typed).
const knownFormats: Record<string, string[]> = {
  music: ["flac", "mp3", "m4a", "opus", "wav"],
};

// FormatChips: the quality profile's format list as ordered chips —
// best-preferred first, ‹ › to reorder, ✕ to remove, and a suggestion-backed
// input to add more.
function FormatChips({
  value,
  onChange,
  suggestions,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  suggestions: string[];
}) {
  const [entry, setEntry] = useState("");

  const add = (raw: string) => {
    const f = raw.trim().toLowerCase().replace(/^\./, "");
    setEntry("");
    if (f && !value.includes(f)) onChange([...value, f]);
  };

  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= value.length) return;
    const next = [...value];
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };

  return (
    <div className="chips">
      {value.map((f, i) => (
        <span key={f} className="chip">
          <button
            type="button"
            className="chip-btn"
            disabled={i === 0}
            aria-label={`Prefer ${f} more`}
            title="Prefer more"
            onClick={() => move(i, -1)}
          >
            ‹
          </button>
          <span className="chip-label">{f}</span>
          <button
            type="button"
            className="chip-btn"
            disabled={i === value.length - 1}
            aria-label={`Prefer ${f} less`}
            title="Prefer less"
            onClick={() => move(i, 1)}
          >
            ›
          </button>
          <button
            type="button"
            className="chip-btn chip-x"
            aria-label={`Remove ${f}`}
            title="Remove"
            onClick={() => onChange(value.filter((x) => x !== f))}
          >
            ✕
          </button>
        </span>
      ))}
      <input
        className="chip-entry"
        list="format-suggestions"
        placeholder="+ add format"
        value={entry}
        onChange={(e) => setEntry(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add(entry);
          }
        }}
        onBlur={() => entry && add(entry)}
      />
      <datalist id="format-suggestions">
        {suggestions
          .filter((s) => !value.includes(s))
          .map((s) => (
            <option key={s} value={s} />
          ))}
      </datalist>
    </div>
  );
}

function QualityProfilesCard({
  onError,
  activeTypes,
}: {
  onError: (message: string) => void;
  activeTypes: string[];
}) {
  const profileTypes = activeTypes.length > 0 ? activeTypes : ["music"];
  const defaultFormats: Record<string, string[]> = {
    music: ["flac", "mp3", "m4a"],
  };
  // Music releases range from a single lossy track to a many-GB lossless
  // discography pack, so the size bounds default wide (1 MB–4 GB, matching
  // the seeded "Standard Music" profile) rather than the backend's generic
  // fallback (20 KB–500 MB), which would silently reject most FLAC albums.
  const defaultMinSizeMB = 1;
  const defaultMaxSizeMB = 4096;
  const [profiles, setProfiles] = useState<QualityProfile[]>([]);
  const [name, setName] = useState("");
  const [profileType, setProfileType] = useState("music");
  const [formats, setFormats] = useState<string[]>(defaultFormats.music);
  const [language, setLanguage] = useState("english");
  const [upgrades, setUpgrades] = useState(false);
  const [minSizeMB, setMinSizeMB] = useState(defaultMinSizeMB);
  const [maxSizeMB, setMaxSizeMB] = useState(defaultMaxSizeMB);
  // Edit-in-place: the saved profile loaded into the form, or null when adding.
  const [editing, setEditing] = useState<QualityProfile | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .listProfiles()
      .then(setProfiles)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  const run = (action: () => Promise<unknown>) => {
    setBusy(true);
    setNotice("");
    action()
      .then(reload)
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const startEdit = (p: QualityProfile) => {
    setEditing(p);
    setName(p.name);
    setProfileType(p.mediaType);
    setFormats(p.formats);
    setLanguage(p.language);
    setUpgrades(p.upgradesAllowed);
    setMinSizeMB(p.minSize > 0 ? p.minSize / (1 << 20) : defaultMinSizeMB);
    setMaxSizeMB(p.maxSize > 0 ? p.maxSize / (1 << 20) : defaultMaxSizeMB);
    setNotice("");
  };

  const cancelEdit = () => {
    setEditing(null);
    setName("");
    setFormats(defaultFormats[profileType] ?? []);
    setUpgrades(false);
    setMinSizeMB(defaultMinSizeMB);
    setMaxSizeMB(defaultMaxSizeMB);
    setNotice("");
  };

  const add = () => {
    const fields = {
      name: name.trim(),
      mediaType: profileType,
      formats,
      language,
      upgradesAllowed: upgrades,
      minSize: Math.round(minSizeMB * (1 << 20)),
      maxSize: Math.round(maxSizeMB * (1 << 20)),
    };
    if (editing) {
      run(() =>
        api.updateProfile({ ...editing, ...fields }).then(() => {
          setEditing(null);
          setName("");
        }),
      );
    } else {
      run(() =>
        api.addProfile({ ...fields, retailBonus: 25 }).then(() => setName("")),
      );
    }
  };

  return (
    <section className="card">
      <h2>Quality Profiles</h2>
      <p className="muted">
        Which release formats are grabbable, best first — release search
        rejects formats a profile doesn't list. The <strong>default</strong>{" "}
        profile drives scoring; per-author profiles come later.
      </p>

      <ul className="rows">
        {profiles.map((p) => (
          <li key={p.id}>
            <div className="row">
              <span className="saved-main">
                <span className="saved-head">
                  <strong>{p.name}</strong>
                  <span className="pill">{p.mediaType}</span>
                  {p.isDefault && <span className="owned yes">default</span>}
                </span>
                <span className="muted saved-sub">
                  {p.formats.join(" › ")}
                  {p.language ? ` · ${p.language}` : " · any language"}
                  {" · "}
                  {formatBytes(p.minSize)}–{formatBytes(p.maxSize)}
                </span>
              </span>
              <span className="row-actions">
                <button
                  className={p.upgradesAllowed ? "toggle on" : "toggle"}
                  disabled={busy}
                  title="When on, owning a lesser format keeps the book wanted until the profile's best format"
                  onClick={() => run(() => api.updateProfile({ ...p, upgradesAllowed: !p.upgradesAllowed }))}
                >
                  {p.upgradesAllowed ? "upgrades on" : "upgrades off"}
                </button>
                <button
                  className={editing?.id === p.id ? "toggle on" : "toggle"}
                  disabled={busy}
                  title="Load this profile into the form below to change its formats, language, or name"
                  onClick={() => (editing?.id === p.id ? cancelEdit() : startEdit(p))}
                >
                  edit
                </button>
                {!p.isDefault && (
                  <>
                    <button
                      className="toggle"
                      disabled={busy}
                      onClick={() => run(() => api.setDefaultProfile(p.id))}
                    >
                      make default
                    </button>
                    <button
                      className="danger"
                      disabled={busy}
                      onClick={() => run(() => api.deleteProfile(p.id))}
                    >
                      remove
                    </button>
                  </>
                )}
              </span>
            </div>
          </li>
        ))}
      </ul>

      <h3 className="settings-subhead">
        {editing ? `Edit ${editing.name}` : "Add a quality profile"}
      </h3>
      <div className="settings-form">
        <label>
          Name
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          Media type
          <select
            value={profileType}
            onChange={(e) => {
              setProfileType(e.target.value);
              setFormats(defaultFormats[e.target.value] ?? []);
            }}
          >
            {profileTypes.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
        <label>
          Formats (best first — reorder with ‹ ›)
          <FormatChips
            value={formats}
            onChange={setFormats}
            suggestions={knownFormats[profileType] ?? []}
          />
        </label>
        <label>
          <span>
            <input
              type="checkbox"
              checked={upgrades}
              onChange={(e) => setUpgrades(e.target.checked)}
            />{" "}
            Allow upgrades (keep wanted until the best format is owned)
          </span>
        </label>
        <label>
          Language
          <select value={language} onChange={(e) => setLanguage(e.target.value)}>
            <option value="">Any language</option>
            <option value="english">English</option>
            <option value="german">German</option>
            <option value="french">French</option>
            <option value="spanish">Spanish</option>
            <option value="italian">Italian</option>
            <option value="dutch">Dutch</option>
            <option value="russian">Russian</option>
            <option value="portuguese">Portuguese</option>
            <option value="polish">Polish</option>
            <option value="japanese">Japanese</option>
          </select>
          <p className="muted field-note">
            Rejects a release whose name states a different language than
            this; a release that states none always passes.
          </p>
        </label>
        <label>
          Size bounds (MB)
          <span className="token-row">
            <input
              type="number"
              min={0}
              step="any"
              value={minSizeMB}
              onChange={(e) => setMinSizeMB(Number(e.target.value) || 0)}
            />
            <span className="muted">to</span>
            <input
              type="number"
              min={0}
              step="any"
              value={maxSizeMB}
              onChange={(e) => setMaxSizeMB(Number(e.target.value) || 0)}
            />
          </span>
          <p className="muted field-note">
            Rejects a release outside this range — catches spam/sample-sized
            fakes on the low end and runaway packs on the high end. A single
            lossless album often runs 200–500 MB; a multi-disc or
            discography pack can run several GB.
          </p>
        </label>
        <div className="settings-actions">
          <button disabled={busy || !name.trim() || formats.length === 0} onClick={add}>
            {editing ? "Save changes" : "Add profile"}
          </button>
          {editing && (
            <button className="toggle" disabled={busy} onClick={cancelEdit}>
              Cancel
            </button>
          )}
          {notice && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
              {notice}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}

const emptyIndexer: Omit<Indexer, "id" | "addedAt"> = {
  name: "",
  type: "torznab",
  baseUrl: "",
  apiKey: "",
  audioCategories: "3010,3040",
  enabled: true,
  priority: 25,
};

function IndexersCard({
  onError,
}: {
  onError: (message: string) => void;
}) {
  const { confirmDlg } = useUi();
  const [indexers, setIndexers] = useState<Indexer[]>([]);
  const [natives, setNatives] = useState<NativeIndexer[]>([]);
  const [draft, setDraft] = useState(emptyIndexer);
  // Edit-in-place: the saved indexer loaded into the form, or null when adding.
  const [editing, setEditing] = useState<Indexer | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .listIndexers()
      .then(setIndexers)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);
  // Built-in native sources, offered as extra "types" in the add form.
  useEffect(() => {
    api.listNativeIndexers().then(setNatives).catch(() => setNatives([]));
  }, []);

  // The native definition backing the current draft type, if any — drives which
  // fields the form shows (native sources have no Newznab/Torznab URL).
  const nativeDef = natives.find((n) => n.name === draft.type);

  const set = (patch: Partial<typeof emptyIndexer>) =>
    setDraft((d) => ({ ...d, ...patch }));

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

  const testDraft = () => {
    setBusy(true);
    setNotice("");
    api
      .testIndexer(draft)
      .then(() => setNotice("✓ Connection OK"))
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const add = () => {
    setBusy(true);
    setNotice("");
    const action = editing
      ? api.updateIndexer({ ...editing, ...draft }).then(() => {
          setNotice(`✓ ${draft.name} saved`);
          setEditing(null);
        })
      : api.addIndexer(draft).then(() => setNotice("✓ Indexer added"));
    action
      .then(() => {
        setDraft(emptyIndexer);
        reload();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const startEdit = (ind: Indexer) => {
    setEditing(ind);
    setDraft({ ...ind });
    setNotice("");
  };

  const cancelEdit = () => {
    setEditing(null);
    setDraft(emptyIndexer);
    setNotice("");
  };

  const toggle = (ind: Indexer) =>
    run(() => api.updateIndexer({ ...ind, enabled: !ind.enabled }));

  const remove = async (ind: Indexer) => {
    const ok = await confirmDlg({
      message: `Remove indexer ${ind.name}?`,
      confirmLabel: "Remove",
      danger: true,
    });
    if (ok) run(() => api.deleteIndexer(ind.id));
  };

  const draftValid = nativeDef
    ? draft.name.trim() !== "" &&
      (!nativeDef.needsApiKey || draft.apiKey.trim() !== "") &&
      // Site URLs are optional, but every entered one (primary + fallbacks,
      // comma-separated) must be a real http(s) URL.
      draft.baseUrl
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean)
        .every((u) => /^https?:\/\//.test(u))
    : draft.name.trim() !== "" && /^https?:\/\//.test(draft.baseUrl.trim());

  return (
    <section className="card">
      <h2>Indexers</h2>
      <p className="muted">
        Newznab (usenet) and Torznab (torrents — Prowlarr/Jackett feeds work)
        endpoints. Add them here by hand, or add CantiNode to Prowlarr as a{" "}
        <strong>Readarr</strong> application and Prowlarr keeps them in sync.
        Categories default to audio (<code>3010,3040</code>); change only for
        an unusual indexer.
      </p>

      {indexers.length > 0 && (
        <ul className="rows">
          {indexers.map((ind) => (
            <li key={ind.id}>
              <div className="row">
                <span className="saved-main">
                  <span className="saved-head">
                    <strong>{ind.name}</strong>
                    {(() => {
                      const nd = natives.find((n) => n.name === ind.type);
                      if (nd) {
                        return (
                          <span className="pill" title={`native source: ${nd.name}`}>
                            🧩 {nd.displayName}
                          </span>
                        );
                      }
                      return (
                        <span className="pill" title={ind.type}>
                          {ind.type === "torznab" ? "🧲 torrent" : "📡 usenet"}
                        </span>
                      );
                    })()}
                    <span className="pill" title="Priority — lower wins ties">
                      prio {ind.priority}
                    </span>
                    {!ind.enabled && <span className="pill off">disabled</span>}
                  </span>
                  <span className="muted file-path saved-sub">
                    {ind.baseUrl || (natives.some((n) => n.name === ind.type) ? "built-in source" : "")}
                  </span>
                </span>
                <span className="row-actions">
                  <button
                    className="toggle"
                    disabled={busy}
                    title="Check the saved connection still works"
                    onClick={() => run(async () => {
                      await api.testIndexer(ind);
                    }, `✓ ${ind.name}: connection OK`)}
                  >
                    test
                  </button>
                  <button
                    className={ind.enabled ? "toggle on" : "toggle"}
                    disabled={busy}
                    onClick={() => toggle(ind)}
                  >
                    {ind.enabled ? "enabled" : "disabled"}
                  </button>
                  <button
                    className={editing?.id === ind.id ? "toggle on" : "toggle"}
                    disabled={busy}
                    title="Load this indexer into the form below to change its URL, key, categories, or priority"
                    onClick={() => (editing?.id === ind.id ? cancelEdit() : startEdit(ind))}
                  >
                    edit
                  </button>
                  <button className="danger" disabled={busy} onClick={() => remove(ind)}>
                    remove
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}

      <h3 className="settings-subhead">
        {editing
          ? `Edit ${editing.name}`
          : indexers.length > 0
            ? "Add another indexer"
            : "Add an indexer"}
      </h3>
      <div className="settings-form">
        <label>
          Name
          <input value={draft.name} onChange={(e) => set({ name: e.target.value })} />
        </label>
        <label>
          Type
          <select
            value={draft.type}
            onChange={(e) => {
              const type = e.target.value;
              const native = natives.find((n) => n.name === type);
              // Switching to a native source clears the Newznab/Torznab URL and
              // seeds any default site URL; switching back restores a blank.
              set({ type, baseUrl: native?.defaultBaseUrl ?? "", apiKey: "" });
            }}
          >
            <option value="torznab">Torznab (torrents)</option>
            <option value="newznab">Newznab (usenet)</option>
            {natives.length > 0 && (
              <optgroup label="Native sources (no API — scraped)">
                {natives.map((n) => (
                  <option key={n.name} value={n.name}>
                    {n.displayName}
                    {n.wip ? " ⚠ WIP" : ""} ({n.mediaTypes.join(", ") || "all"})
                  </option>
                ))}
              </optgroup>
            )}
          </select>
        </label>
        {nativeDef ? (
          <>
            {nativeDef.wip && (
              <p className="notice bad field-note">
                ⚠ <strong>Work in progress.</strong> {nativeDef.displayName} is
                experimental and wonky — scraping these sites reliably still
                needs work, so expect failed searches and grabs. Enable it to
                try it, not to depend on it.
              </p>
            )}
            <p className="muted field-note">
              <strong>{nativeDef.displayName}</strong> is a built-in scraped
              source — no Newznab/Torznab endpoint. It's off until you enable it,
              user-configured, and yours to use responsibly; it stays hidden from
              Prowlarr. Serves: {nativeDef.mediaTypes.join(", ") || "all media"}.
            </p>
            {nativeDef.defaultBaseUrl !== "" &&
              (() => {
                // The base-URL field stores "primary,fallback"; the form edits
                // them as two inputs (the site runs mirror domains).
                const parts = draft.baseUrl.split(",").map((s) => s.trim());
                const primary = parts[0] ?? "";
                const fallback = parts[1] ?? "";
                const join = (p: string, f: string) =>
                  set({ baseUrl: [p.trim(), f.trim()].filter(Boolean).join(",") });
                return (
                  <>
                    <label>
                      Site URL (its domain rotates — override when it moves)
                      <input
                        placeholder={nativeDef.defaultBaseUrl}
                        value={primary}
                        onChange={(e) => join(e.target.value, fallback)}
                      />
                    </label>
                    <label>
                      Fallback site URL (optional — a mirror, tried when the main site fails)
                      <input
                        placeholder="https://mirror.example"
                        value={fallback}
                        onChange={(e) => join(primary, e.target.value)}
                      />
                    </label>
                  </>
                );
              })()}
            <label>
              {nativeDef.needsApiKey
                ? "API key / membership token"
                : "API key / membership token (optional — some sources are search-only without one)"}
              <input value={draft.apiKey} onChange={(e) => set({ apiKey: e.target.value })} />
            </label>
          </>
        ) : (
          <>
            <label>
              URL
              <input
                placeholder="https://indexer.example (or a Prowlarr/Jackett feed URL)"
                value={draft.baseUrl}
                onChange={(e) => set({ baseUrl: e.target.value })}
              />
            </label>
            <label>
              API key
              <input value={draft.apiKey} onChange={(e) => set({ apiKey: e.target.value })} />
            </label>
          </>
        )}
        {!nativeDef && (
          <label>
            Audio categories
            <input
              title="3010 = Audio/MP3, 3040 = Audio/Lossless"
              value={draft.audioCategories}
              onChange={(e) => set({ audioCategories: e.target.value })}
            />
          </label>
        )}
        <label>
          Priority (1–50, lower wins ties)
          <input
            type="number"
            min={1}
            max={50}
            value={draft.priority}
            onChange={(e) => set({ priority: Number(e.target.value) || 25 })}
          />
        </label>
        <div className="settings-actions">
          <button disabled={busy || !draftValid} onClick={testDraft}>
            Test
          </button>
          <button disabled={busy || !draftValid} onClick={add}>
            {editing ? "Save changes" : "Add"}
          </button>
          {editing && (
            <button className="toggle" disabled={busy} onClick={cancelEdit}>
              Cancel
            </button>
          )}
          {notice && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
              {notice}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}

function NamingCard({
  onError,
}: {
  onError: (message: string) => void;
}) {
  const [settings, setSettings] = useState<NamingSettings | null>(null);
  const [t, setT] = useState<Partial<NamingSettings>>({});
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .getNamingSettings()
      .then((s) => {
        setSettings(s);
        setT(s);
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!settings) return null;

  const save = () => {
    setBusy(true);
    setNotice("");
    api
      .saveNamingSettings(t)
      .then((s) => {
        setSettings(s);
        setT(s);
        setNotice("✓ Saved — use Organize on a library page to apply to existing files");
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const field = (label: string, key: keyof NamingSettings, title?: string) => (
    <label>
      {label}
      <input
        title={title}
        value={(t[key] as string) ?? ""}
        onChange={(e) => setT({ ...t, [key]: e.target.value })}
      />
    </label>
  );

  return (
    <section className="card">
      <h2>File Naming</h2>
      <p className="muted">
        How organized files are placed inside a music root folder.
      </p>
      <div className="settings-form">
        <Section title="Music">
          {field(
            "File template",
            "musicFile",
            "Renders one full path (folders included) from a matched track's artist/album/title: {Artist} {Album} {Year} {TrackNumber} {DiscNumber} {Title} {Ext}",
          )}
          <p className="muted field-note">
            Tokens: <code>{"{Artist}"}</code> <code>{"{Album}"}</code>{" "}
            <code>{"{Year}"}</code> <code>{"{TrackNumber}"}</code>{" "}
            <code>{"{DiscNumber}"}</code> <code>{"{Title}"}</code>{" "}
            <code>{"{Ext}"}</code>. "/" separators create subfolders. Tokens
            without a value drop out cleanly; an emptied field reverts to
            the default.
          </p>
          <p className="muted field-note">
            Example: <code>{settings.musicExample}</code>
          </p>
        </Section>
        <div className="settings-actions">
          <button disabled={busy} onClick={save}>
            Save
          </button>
          {notice && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>
              {notice}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}


function RootFoldersCard({
  onError,
  onChanged,
}: {
  onError: (message: string) => void;
  onChanged?: () => void;
}) {
  const { confirmDlg } = useUi();
  const [folders, setFolders] = useState<RootFolder[]>([]);
  const [path, setPath] = useState("");
  const [browsing, setBrowsing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  const reload = useCallback(() => {
    api
      .listRootFolders()
      .then(setFolders)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  useEffect(reload, [reload]);

  const add = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = path.trim();
    if (!trimmed) return;
    setBusy(true);
    setNotice("");
    api
      .addRootFolder("music", trimmed)
      .then(() => {
        setPath("");
        reload();
        onChanged?.();
      })
      .catch((err: unknown) =>
        setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`),
      )
      .finally(() => setBusy(false));
  };

  const remove = async (f: RootFolder) => {
    const ok = await confirmDlg({
      title: "Remove root folder",
      message: `Remove root folder ${f.path}? Files on disk are not touched.`,
      confirmLabel: "Remove folder",
      danger: true,
    });
    if (!ok) return;
    api
      .deleteRootFolder(f.id)
      .then(() => {
        reload();
        onChanged?.();
      })
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  };

  return (
    <section className="card">
      <h2>Root Folders</h2>
      <p className="muted">
        Where your libraries live on disk. The scanner walks these to match
        files you already own; note the path must exist on the machine running
        CantiNode (in WSL, Windows drives are under <code>/mnt/c/…</code>).
      </p>

      {folders.length > 0 && (
        <ul className="rows">
          {folders.map((f) => (
            <li key={f.id}>
              <div className="row">
                <span className="file-path">
                  {f.path}
                  {!f.accessible && <span className="notice bad"> (not accessible)</span>}
                </span>
                <span className="row-actions">
                  <span className="muted">{f.mediaType}</span>
                  <button className="danger" onClick={() => remove(f)}>
                    remove
                  </button>
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}

      <h3 className="settings-subhead">Add a root folder</h3>
      <form onSubmit={add} className="search-form">
        <input
          placeholder="/data/music or /mnt/c/Users/…/Music"
          value={path}
          onChange={(e) => setPath(e.target.value)}
        />
        <button
          type="button"
          className="toggle"
          onClick={() => setBrowsing(!browsing)}
          title="Pick the folder visually on the server's filesystem"
        >
          {browsing ? "Hide browser" : "Browse…"}
        </button>
        <button type="submit" disabled={busy}>
          Add
        </button>
      </form>
      {browsing && (
        <FolderBrowser
          initial={path}
          onPick={(p) => {
            setPath(p);
            setBrowsing(false);
          }}
          onClose={() => setBrowsing(false)}
        />
      )}
      {notice && <p className="notice bad">{notice}</p>}
    </section>
  );
}

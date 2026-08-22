import { useCallback, useEffect, useState } from "react";
import {
  api,
  ApiError,
  getApiKey,
  setApiKey,
  type AuthStatus,
  type HealthIssue,
} from "./api";
import SetupWizard from "./components/SetupWizard";
import { getThemePref, setThemePref, type ThemePref } from "./theme";
import { UiProvider, useUi } from "./ui";
import ActivityView from "./views/ActivityView";
import AlbumDetailView from "./views/AlbumDetailView";
import ArtistDetailView from "./views/ArtistDetailView";
import MusicLibraryView from "./views/MusicLibraryView";
import SearchView from "./views/SearchView";
import SettingsView from "./views/SettingsView";
import SystemView from "./views/SystemView";
import UnmatchedFilesView from "./views/UnmatchedFilesView";
import "./App.css";

// Music is the only library — Plex-style, its nav entry appears once a
// music root folder is set up (see hasMusicRoot).
type Page =
  | { name: "library" }
  | { name: "artist"; id: number }
  | { name: "album"; id: number; artistId: number }
  | { name: "unmatched" }
  | { name: "search"; q: string }
  | { name: "activity" }
  | { name: "settings" }
  | { name: "system" };

// Hash routing: every page has a URL (#/artist/34, #/search?q=…), so refresh
// keeps the page, back/forward work, and any view can be bookmarked or
// shared. The hash is the single source of truth — navigation writes it, a
// hashchange listener drives the page state.
function pageToHash(p: Page): string {
  switch (p.name) {
    case "library":
      return "#/";
    case "artist":
      return `#/artist/${p.id}`;
    case "album":
      return `#/album/${p.id}?artist=${p.artistId}`;
    case "search":
      return `#/search?q=${encodeURIComponent(p.q)}`;
    default:
      return `#/${p.name}`;
  }
}

function hashToPage(hash: string): Page {
  const [path, query] = hash.replace(/^#\/?/, "").split("?");
  const q = new URLSearchParams(query ?? "");
  const seg = path.split("/").filter(Boolean);
  const id = Number(seg[1]);
  switch (seg[0]) {
    case undefined:
      return { name: "library" };
    case "artist":
      return id > 0 ? { name: "artist", id } : { name: "library" };
    case "album":
      return id > 0
        ? { name: "album", id, artistId: Number(q.get("artist")) || 0 }
        : { name: "library" };
    case "unmatched":
      return { name: "unmatched" };
    case "search":
      return { name: "search", q: q.get("q") ?? "" };
    case "activity":
      return { name: "activity" };
    case "settings":
      return { name: "settings" };
    case "system":
      return { name: "system" };
    default:
      return { name: "library" };
  }
}

export default function App() {
  return (
    <UiProvider>
      <AppInner />
    </UiProvider>
  );
}

function AppInner() {
  const { toast } = useUi();
  const [key, setKey] = useState(getApiKey());
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [setupNeeded, setSetupNeeded] = useState<boolean | null>(null);
  const [connected, setConnected] = useState(false);
  const [hasMusicRoot, setHasMusicRoot] = useState(false);
  const [health, setHealth] = useState<HealthIssue[]>([]);
  const [page, setPage] = useState<Page>(() => hashToPage(location.hash));
  // The connection error keeps its dedicated card (it carries recovery UI);
  // every in-app error surfaces as a toast instead.
  const [error, setError] = useState("");
  const onError = useCallback((message: string) => toast(message, "bad"), [toast]);

  // Login sessions replace the API-key prompt once an account is set up
  // (Settings → General → Security); without one, the key prompt remains.
  // A fresh instance skips both: the first-run wizard claims it with a new
  // account, no API key required.
  useEffect(() => {
    api
      .authStatus()
      .then(setAuth)
      .catch(() => setAuth({ authEnabled: false, authenticated: false }));
    api
      .setupStatus()
      .then((s) => setSetupNeeded(s.needed))
      .catch(() => setSetupNeeded(false));
  }, []);

  // Music's nav entry appears once a music root folder is set up.
  const reloadLibraries = useCallback(() => {
    api
      .listRootFolders()
      .then((folders) => setHasMusicRoot(folders.some((f) => f.mediaType === "music")))
      .catch(() => setHasMusicRoot(false));
  }, []);

  const reloadHealth = useCallback(() => {
    api
      .health()
      .then((h) => setHealth(h.issues))
      .catch(() => {}); // the banner is best-effort; never blocks the UI
  }, []);

  // Checks run server-side every 15 min; poll the cached result so the
  // banner appears/clears without a reload.
  useEffect(() => {
    if (!connected) return;
    reloadHealth();
    const timer = setInterval(reloadHealth, 60_000);
    return () => clearInterval(timer);
  }, [connected, reloadHealth]);

  useEffect(() => {
    if (!auth) return; // auth status still loading
    const ready = auth.authEnabled ? auth.authenticated : !!key;
    if (!ready) return;
    setError("");
    api
      .systemStatus()
      .then(() => {
        setConnected(true);
        reloadLibraries();
      })
      .catch((err: unknown) => {
        setConnected(false);
        setError(err instanceof ApiError ? err.message : String(err));
      });
  }, [auth, key, reloadLibraries]);

  // API-key access (no login system, or not yet signed in via session) is
  // root-equivalent, matching the backend's requireAdmin check; a signed-in
  // session is admin only if its account role says so.
  const isAdmin = !auth?.authEnabled || auth.role === "admin";

  // Back/forward and hand-edited URLs drive the page through the hash.
  useEffect(() => {
    const onHash = () => setPage(hashToPage(location.hash));
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const go = (p: Page) => {
    setError("");
    const target = pageToHash(p);
    if (location.hash === target) {
      setPage(p); // same-URL navigation still re-renders the page
    } else {
      location.hash = target; // hashchange listener updates the state
    }
    reloadLibraries(); // library activity can change after adds/scans
  };

  const navButton = (p: Page, label: string, icon: string) => {
    const current = page.name === p.name;
    return (
      <button
        key={label}
        aria-current={current ? "page" : undefined}
        className={current ? "nav-item active" : "nav-item"}
        onClick={() => go(p)}
      >
        <span className="nav-icon">{icon}</span> {label}
      </button>
    );
  };

  return (
    <div className={connected ? "app with-sidebar" : "app"}>
      {connected && (
        <aside className="sidebar">
          <h1 className="brand">🎵 CantiNode</h1>
          <SidebarSearch onSearch={(q) => go({ name: "search", q })} />
          <nav>
            {hasMusicRoot && <div className="nav-group">Library</div>}
            {hasMusicRoot && navButton({ name: "library" }, "Music", "🎵")}
            {hasMusicRoot && navButton({ name: "unmatched" }, "Unmatched Files", "❓")}
            <div className="nav-group">App</div>
            {navButton({ name: "activity" }, "Activity", "⬇️")}
            {isAdmin && navButton({ name: "settings" }, "Settings", "⚙️")}
            {isAdmin && navButton({ name: "system" }, "System", "🖥️")}
            <ThemeButton />
            {auth?.authEnabled && auth.authenticated && (
              <button
                className="nav-item"
                onClick={() => {
                  api
                    .logout()
                    .catch(() => {})
                    .finally(() => location.reload());
                }}
              >
                <span className="nav-icon">🚪</span> Log out
              </button>
            )}
          </nav>
        </aside>
      )}

      <main className="content">
        {!connected && <h1 className="brand">🎵 CantiNode</h1>}

        {setupNeeded && !connected && (
          <SetupWizard
            onDone={() => {
              setSetupNeeded(false);
              api.authStatus().then(setAuth).catch(() => setAuth({ authEnabled: true, authenticated: true }));
            }}
          />
        )}

        {setupNeeded === false && auth?.authEnabled && !auth.authenticated && (
          <section className="card auth-card">
            <h2>Sign in</h2>
            <p className="muted">Welcome back — sign in to your CantiNode.</p>
            <LoginForm
              onLoggedIn={() =>
                api.authStatus().then(setAuth).catch(() => setAuth({ authEnabled: true, authenticated: true }))
              }
            />
          </section>
        )}

        {setupNeeded === false && auth && !auth.authEnabled && !key && (
          <section className="card auth-card">
            <h2>Connect</h2>
            <p className="muted">
              Paste the API key from <code>config.yaml</code> in your CantiNode
              data directory. (You can set up a login account later under
              Settings → General → Security.)
            </p>
            <ApiKeyForm onSave={setKey} />
          </section>
        )}

        {connected && health.length > 0 && (
          <section className="card health-banner">
            {health.map((issue, i) => (
              <p key={i} className={issue.level === "error" ? "health-issue error" : "health-issue"}>
                {issue.level === "error" ? "⛔" : "⚠️"} {issue.message}
              </p>
            ))}
          </section>
        )}

        {error && (
          <section className="card error">
            <p>{error}</p>
            {!connected && key && (
              <button
                onClick={() => {
                  setApiKey("");
                  setKey("");
                  setError("");
                }}
              >
                Change API key
              </button>
            )}
          </section>
        )}

        {connected && page.name === "library" && (
          <MusicLibraryView
            onError={onError}
            onOpenArtist={(id) => go({ name: "artist", id })}
          />
        )}
        {connected && page.name === "artist" && (
          <ArtistDetailView
            id={page.id}
            isAdmin={isAdmin}
            onError={onError}
            onBack={() => go({ name: "library" })}
            onOpenAlbum={(albumId) => go({ name: "album", id: albumId, artistId: page.id })}
          />
        )}
        {connected && page.name === "album" && (
          <AlbumDetailView
            key={page.id}
            id={page.id}
            onError={onError}
            onBack={() =>
              page.artistId > 0
                ? go({ name: "artist", id: page.artistId })
                : go({ name: "library" })
            }
          />
        )}
        {connected && page.name === "unmatched" && <UnmatchedFilesView onError={onError} />}
        {connected && page.name === "search" && (
          <SearchView
            key={page.q}
            query={page.q}
            onError={onError}
            onOpenArtist={(id) => go({ name: "artist", id })}
          />
        )}
        {connected && page.name === "activity" && <ActivityView onError={onError} />}
        {connected && page.name === "settings" && (
          <SettingsView isAdmin={isAdmin} onError={onError} onLibrariesChanged={reloadLibraries} />
        )}
        {connected && isAdmin && page.name === "system" && <SystemView onError={onError} />}
      </main>
    </div>
  );
}

// ThemeButton cycles the display theme: Auto (follow the OS) → Light → Dark.
// A per-browser preference, so it lives in the sidebar where every account —
// member or admin — can reach it.
function ThemeButton() {
  const [pref, setPref] = useState<ThemePref>(getThemePref);
  const next: Record<ThemePref, ThemePref> = { auto: "light", light: "dark", dark: "auto" };
  const face: Record<ThemePref, { icon: string; label: string }> = {
    auto: { icon: "🌗", label: "Theme: Auto" },
    light: { icon: "☀️", label: "Theme: Light" },
    dark: { icon: "🌙", label: "Theme: Dark" },
  };
  return (
    <button
      className="nav-item"
      title="Switch between Auto (follow your system), Light, and Dark"
      onClick={() => {
        const p = next[pref];
        setThemePref(p);
        setPref(p);
      }}
    >
      <span className="nav-icon">{face[pref].icon}</span> {face[pref].label}
    </button>
  );
}

// SidebarSearch: the global search box — Enter searches every library.
function SidebarSearch({ onSearch }: { onSearch: (q: string) => void }) {
  const [q, setQ] = useState("");
  return (
    <form
      className="sidebar-search"
      onSubmit={(e) => {
        e.preventDefault();
        if (q.trim()) onSearch(q.trim());
      }}
    >
      <input
        placeholder="🔍 Search your library…"
        aria-label="Search your library"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
    </form>
  );
}

function LoginForm({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!username.trim() || !password) return;
        setBusy(true);
        setNotice("");
        api
          .login(username.trim(), password)
          .then(onLoggedIn)
          .catch((err: unknown) =>
            setNotice(err instanceof Error ? err.message : String(err)),
          )
          .finally(() => setBusy(false));
      }}
    >
      <input
        placeholder="Username"
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        autoFocus
      />
      <input
        type="password"
        placeholder="Password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <button type="submit" disabled={busy || !username.trim() || !password}>
        {busy ? "Signing in…" : "Sign in"}
      </button>
      {notice && <span className="notice bad">{notice}</span>}
    </form>
  );
}

function ApiKeyForm({ onSave }: { onSave: (key: string) => void }) {
  const [value, setValue] = useState("");
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const trimmed = value.trim();
        if (!trimmed) return;
        setApiKey(trimmed);
        onSave(trimmed);
      }}
    >
      <input
        type="password"
        placeholder="API key"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoFocus
      />
      <button type="submit">Save</button>
    </form>
  );
}

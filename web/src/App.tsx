import { useCallback, useEffect, useState } from "react";
import {
  api,
  ApiError,
  getApiKey,
  setApiKey,
  type AuthStatus,
  type HealthIssue,
  type LibraryStatus,
} from "./api";
import SetupWizard from "./components/SetupWizard";
import { getThemePref, setThemePref, type ThemePref } from "./theme";
import { UiProvider, useUi } from "./ui";
import ActivityView from "./views/ActivityView";
import AuthorDetailView from "./views/AuthorDetailView";
import BookDetailView from "./views/BookDetailView";
import BooksLibraryView from "./views/BooksLibraryView";
import CalendarView from "./views/CalendarView";
import HomeView from "./views/HomeView";
import SearchView from "./views/SearchView";
import SeriesDetailView from "./views/SeriesDetailView";
import SeriesLibraryView from "./views/SeriesLibraryView";
import SettingsView from "./views/SettingsView";
import SystemView from "./views/SystemView";
import "./App.css";

// Plex-style navigation: Home, then one entry per *active* library (a media
// type appears only once its library is set up), then the app-level pages.
type Page =
  | { name: "home" }
  | { name: "library"; mediaType: string }
  | { name: "author"; id: number; library: "ebook" | "audiobook" }
  | { name: "book"; id: number; library: "ebook" | "audiobook"; authorId: number }
  | { name: "series-detail"; id: number; mediaType: string }
  | { name: "search"; q: string }
  | { name: "calendar" }
  | { name: "activity" }
  | { name: "settings" }
  | { name: "system" };

export const libraryLabels: Record<string, string> = {
  ebook: "Ebooks",
  audiobook: "Audiobooks",
  manga: "Manga",
  comic: "Comics",
  magazine: "Magazines",
};

const libraryIcons: Record<string, string> = {
  ebook: "📖",
  audiobook: "🎧",
  manga: "🀄",
  comic: "💥",
  magazine: "📰",
};

// Hash routing: every page has a URL (#/library/manga, #/book/34?lib=ebook…),
// so refresh keeps the page, back/forward work, and any view can be
// bookmarked or shared. The hash is the single source of truth — navigation
// writes it, a hashchange listener drives the page state.
function pageToHash(p: Page): string {
  switch (p.name) {
    case "home":
      return "#/";
    case "library":
      return `#/library/${p.mediaType}`;
    case "author":
      return `#/author/${p.id}?lib=${p.library}`;
    case "book":
      return `#/book/${p.id}?lib=${p.library}&author=${p.authorId}`;
    case "series-detail":
      return `#/series/${p.id}?type=${p.mediaType}`;
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
  const lib = q.get("lib") === "audiobook" ? ("audiobook" as const) : ("ebook" as const);
  const id = Number(seg[1]);
  switch (seg[0]) {
    case undefined:
      return { name: "home" };
    case "library":
      return seg[1] ? { name: "library", mediaType: seg[1] } : { name: "home" };
    case "author":
      return id > 0 ? { name: "author", id, library: lib } : { name: "home" };
    case "book":
      return id > 0
        ? { name: "book", id, library: lib, authorId: Number(q.get("author")) || 0 }
        : { name: "home" };
    case "series":
      return id > 0
        ? { name: "series-detail", id, mediaType: q.get("type") ?? "manga" }
        : { name: "home" };
    case "search":
      return { name: "search", q: q.get("q") ?? "" };
    case "calendar":
      return { name: "calendar" };
    case "activity":
      return { name: "activity" };
    case "settings":
      return { name: "settings" };
    case "system":
      return { name: "system" };
    default:
      return { name: "home" };
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
  const [libraries, setLibraries] = useState<LibraryStatus[]>([]);
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

  const reloadLibraries = useCallback(() => {
    api
      .libraries()
      .then(setLibraries)
      .catch(() => setLibraries([]));
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

  const active = libraries.filter((l) => l.active);
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
    const current =
      page.name === p.name &&
      (p.name !== "library" ||
        (page.name === "library" && page.mediaType === (p as { mediaType?: string }).mediaType));
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
          <h1 className="brand">🖋️ LibriNode</h1>
          <SidebarSearch onSearch={(q) => go({ name: "search", q })} />
          <nav>
            {navButton({ name: "home" }, "Home", "🏠")}
            {active.length > 0 && <div className="nav-group">Libraries</div>}
            {active.map((l) =>
              navButton(
                { name: "library", mediaType: l.mediaType },
                libraryLabels[l.mediaType] ?? l.mediaType,
                libraryIcons[l.mediaType] ?? "📚",
              ),
            )}
            <div className="nav-group">App</div>
            {navButton({ name: "calendar" }, "Calendar", "📅")}
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
        {!connected && <h1 className="brand">🖋️ LibriNode</h1>}

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
            <p className="muted">Welcome back — sign in to your LibriNode.</p>
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
              Paste the API key from <code>config.yaml</code> in your LibriNode
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

        {connected && page.name === "home" && (
          <HomeView
            onError={onError}
            onOpenLibrary={(mediaType) => go({ name: "library", mediaType })}
            onOpenItem={(mediaType, it) => {
              // Prose books open their detail page; volumes/issues live as
              // rows on their series page. Without the id, the library grid.
              if ((mediaType === "ebook" || mediaType === "audiobook") && it.authorId) {
                go({ name: "book", id: it.bookId, library: mediaType, authorId: it.authorId });
              } else if (it.seriesId) {
                go({ name: "series-detail", id: it.seriesId, mediaType });
              } else {
                go({ name: "library", mediaType });
              }
            }}
          />
        )}
        {connected && page.name === "library" &&
          (page.mediaType === "ebook" || page.mediaType === "audiobook" ? (
            <BooksLibraryView
              key={page.mediaType}
              library={page.mediaType as "ebook" | "audiobook"}
              onError={onError}
              onOpenAuthor={(id) =>
                go({ name: "author", id, library: page.mediaType as "ebook" | "audiobook" })
              }
            />
          ) : (
            <SeriesLibraryView
              key={page.mediaType}
              mediaType={page.mediaType}
              onError={onError}
              onOpenSeries={(id) => go({ name: "series-detail", id, mediaType: page.mediaType })}
            />
          ))}
        {connected && page.name === "author" && (
          <AuthorDetailView
            id={page.id}
            library={page.library}
            onError={onError}
            onBack={() => go({ name: "library", mediaType: page.library })}
            onOpenBook={(bookId) =>
              go({ name: "book", id: bookId, library: page.library, authorId: page.id })
            }
          />
        )}
        {connected && page.name === "book" && (
          <BookDetailView
            key={`${page.id}-${page.library}`}
            id={page.id}
            library={page.library}
            onError={onError}
            onBack={() =>
              // Deep-linked book URLs may not carry the author id — fall back
              // to the library grid rather than a broken author page.
              page.authorId > 0
                ? go({ name: "author", id: page.authorId, library: page.library })
                : go({ name: "library", mediaType: page.library })
            }
            onSwitchLibrary={(library) =>
              go({ name: "book", id: page.id, library, authorId: page.authorId })
            }
          />
        )}
        {connected && page.name === "series-detail" && (
          <SeriesDetailView
            id={page.id}
            mediaType={page.mediaType}
            onError={onError}
            onBack={() => go({ name: "library", mediaType: page.mediaType })}
          />
        )}
        {connected && page.name === "search" && (
          <SearchView
            key={page.q}
            query={page.q}
            onError={onError}
            onOpenAuthor={(id, library) => go({ name: "author", id, library })}
            onOpenBook={(b, library) =>
              go({ name: "book", id: b.id, library, authorId: b.authorId })
            }
            onOpenSeries={(id, mediaType) => go({ name: "series-detail", id, mediaType })}
          />
        )}
        {connected && page.name === "calendar" && (
          <CalendarView
            onError={onError}
            onOpenBook={(it) =>
              go({
                name: "book",
                id: it.bookId,
                library: it.mediaType as "ebook" | "audiobook",
                authorId: it.authorId ?? 0,
              })
            }
            onOpenSeries={(it) =>
              go({ name: "series-detail", id: it.seriesId ?? 0, mediaType: it.mediaType })
            }
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
        placeholder="🔍 Search libraries…"
        aria-label="Search all libraries"
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

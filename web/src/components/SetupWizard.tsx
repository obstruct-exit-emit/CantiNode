import { useState } from "react";
import { api, type DownloadClient, type Indexer, type RootFolder } from "../api";
import FolderBrowser from "./FolderBrowser";

// SetupWizard is the first-run experience: a fresh instance is claimed by
// creating a login account (no API key involved), then walked through the
// essentials — library folder, metadata token, indexer, download client.
// Every step after the account is skippable; everything lives in Settings
// afterwards.

const steps = ["Account", "Library", "Metadata", "Indexer", "Downloads", "Done"] as const;

const mediaTypes = ["ebook", "audiobook", "manga", "comic", "magazine"] as const;

export default function SetupWizard({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  // Step 0 — account.
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPw, setConfirmPw] = useState("");

  // Step 1 — root folders.
  const [mediaType, setMediaType] = useState<string>("ebook");
  const [variant, setVariant] = useState("mono");
  const [path, setPath] = useState("");
  const [browsing, setBrowsing] = useState(false);
  const [folders, setFolders] = useState<RootFolder[]>([]);

  // Step 2 — metadata token.
  const [token, setToken] = useState("");

  // Step 3 — indexer.
  const [indexer, setIndexer] = useState<Omit<Indexer, "id" | "addedAt">>({
    name: "",
    type: "newznab",
    baseUrl: "",
    apiKey: "",
    categories: "7000,7020",
    audioCategories: "3030",
    comicCategories: "7030",
    magazineCategories: "7010",
    enabled: true,
    priority: 25,
  });
  const [indexersAdded, setIndexersAdded] = useState(0);

  // Step 4 — download client.
  const [client, setClient] = useState<Omit<DownloadClient, "id">>({
    name: "",
    type: "qbittorrent",
    host: "",
    username: "",
    password: "",
    apiKey: "",
    category: "librinode",
    enabled: true,
    priority: 1,
  });
  const [clientsAdded, setClientsAdded] = useState(0);

  const next = () => {
    setNotice("");
    setStep((s) => Math.min(s + 1, steps.length - 1));
  };
  const back = () => {
    setNotice("");
    setStep((s) => Math.max(s - 1, 0));
  };

  const run = (action: () => Promise<unknown>, done?: string, andNext = false) => {
    setBusy(true);
    setNotice("");
    action()
      .then(() => {
        if (done) setNotice(done);
        if (andNext) next();
      })
      .catch((err: unknown) => setNotice(`✗ ${err instanceof Error ? err.message : String(err)}`))
      .finally(() => setBusy(false));
  };

  const claim = () => {
    if (password !== confirmPw) {
      setNotice("✗ Passwords don't match");
      return;
    }
    run(() => api.setupInstance(username.trim(), password), undefined, true);
  };

  const addFolder = () => {
    const trimmed = path.trim();
    if (!trimmed) return;
    run(async () => {
      const f = await api.addRootFolder(
        mediaType,
        trimmed,
        mediaType === "manga" ? variant : undefined,
      );
      setFolders((list) => [...list, f]);
      setPath("");
    }, "✓ Library folder added — add another, or continue");
  };

  const saveToken = () => {
    run(async () => {
      const s = await api.getMetadataSettings();
      await api.saveMetadataSettings(
        "hardcover",
        { ...s.providers, hardcover: { ...(s.providers.hardcover ?? {}), token: token.trim() } },
        {
          mangaProvider: s.mangaProvider,
          comicProvider: s.comicProvider,
          mangaCoverSource: s.mangaCoverSource,
          comicCoverSource: s.comicCoverSource,
          language: s.language,
          country: s.country,
          includeAdult: s.includeAdult,
        },
      );
    }, undefined, true);
  };

  const testToken = () =>
    run(
      () => api.testMetadataProvider("hardcover", { token: token.trim() }),
      "✓ Token accepted",
    );

  const addIndexer = () =>
    run(async () => {
      await api.addIndexer(indexer);
      setIndexersAdded((n) => n + 1);
      setIndexer({ ...indexer, name: "", baseUrl: "", apiKey: "" });
    }, "✓ Indexer added — add another, or continue");

  const testIndexer = () =>
    run(() => api.testIndexer(indexer), "✓ Connection OK");

  const addClient = () =>
    run(async () => {
      await api.addDownloadClient(client);
      setClientsAdded((n) => n + 1);
      setClient({ ...client, name: "", host: "", username: "", password: "", apiKey: "" });
    }, "✓ Download client added — add another, or continue");

  const testClient = () =>
    run(() => api.testDownloadClient(client), "✓ Connection OK");

  const indexerValid = indexer.name.trim() !== "" && /^https?:\/\//.test(indexer.baseUrl.trim());
  const clientValid = client.name.trim() !== "" && /^https?:\/\//.test(client.host.trim());

  return (
    <section className="card wizard">
      <div className="wizard-dots" aria-hidden>
        {steps.map((label, i) => (
          <span key={label} className={i === step ? "dot active" : i < step ? "dot done" : "dot"} title={label} />
        ))}
      </div>

      {step === 0 && (
        <>
          <h2>Welcome to LibriNode 🖋️</h2>
          <p className="muted">
            Let's get you set up in a couple of minutes. First, create the
            account you'll sign in with — no digging for API keys.
          </p>
          <div className="settings-form">
            <label>
              Username
              <input autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
            </label>
            <label>
              Password (min. 8 characters)
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </label>
            <label>
              Confirm password
              <input type="password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
            </label>
            <div className="settings-actions">
              <button disabled={busy || !username.trim() || password.length < 8} onClick={claim}>
                Create account & continue
              </button>
              {notice && (
                <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</span>
              )}
            </div>
          </div>
        </>
      )}

      {step === 1 && (
        <>
          <h2>Where do your books live?</h2>
          <p className="muted">
            A <strong>root folder</strong> creates a library — LibriNode scans
            it for files you own and organizes new downloads into it. Add one
            per media type you use (paths are on the machine running
            LibriNode; under WSL, Windows drives are at <code>/mnt/c/…</code>).
          </p>
          <div className="settings-form">
            <div className="settings-actions">
              <select value={mediaType} onChange={(e) => setMediaType(e.target.value)}>
                {mediaTypes.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
              {mediaType === "manga" && (
                <select value={variant} onChange={(e) => setVariant(e.target.value)}>
                  <option value="mono">monochrome</option>
                  <option value="color">colorized</option>
                </select>
              )}
              <input
                style={{ flex: 1, minWidth: 220 }}
                placeholder="/data/ebooks"
                value={path}
                onChange={(e) => setPath(e.target.value)}
              />
              <button
                className="toggle"
                onClick={() => setBrowsing(!browsing)}
                title="Pick the folder visually"
              >
                {browsing ? "Hide" : "Browse…"}
              </button>
              <button disabled={busy || !path.trim()} onClick={addFolder}>
                Add
              </button>
            </div>
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
            {folders.length > 0 && (
              <ul className="rows">
                {folders.map((f) => (
                  <li key={f.id}>
                    <div className="row">
                      <span className="file-path">📁 {f.path}</span>
                      <span className="muted">{f.mediaType}</span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}

      {step === 2 && (
        <>
          <h2>Metadata</h2>
          <p className="muted">
            Book search, covers, and author bibliographies come from{" "}
            <strong>Hardcover</strong> — grab a free token at{" "}
            <a href="https://hardcover.app/account/api" target="_blank" rel="noreferrer">
              hardcover.app/account/api
            </a>
            . Manga (AniList) needs no key; a ComicVine key can be added later
            in Settings → Metadata.
          </p>
          <div className="settings-form">
            <label>
              Hardcover API token
              <input
                type="password"
                placeholder="Paste your token"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
            </label>
            <div className="settings-actions">
              <button className="toggle" disabled={busy || !token.trim()} onClick={testToken}>
                Test
              </button>
              <button disabled={busy || !token.trim()} onClick={saveToken}>
                Save & continue
              </button>
              {notice && (
                <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</span>
              )}
            </div>
          </div>
        </>
      )}

      {step === 3 && (
        <>
          <h2>Where to search</h2>
          <p className="muted">
            Add a <strong>Newznab</strong> (usenet) or <strong>Torznab</strong>{" "}
            (torrent) indexer — Prowlarr/Jackett per-indexer feed URLs work
            too. Running <strong>Prowlarr</strong>? You can skip this and add
            LibriNode there as a <em>Readarr</em> application later; it syncs
            all your indexers automatically.
          </p>
          <div className="settings-form">
            <label>
              Name
              <input value={indexer.name} onChange={(e) => setIndexer({ ...indexer, name: e.target.value })} />
            </label>
            <label>
              Type
              <select
                value={indexer.type}
                onChange={(e) => setIndexer({ ...indexer, type: e.target.value as Indexer["type"] })}
              >
                <option value="newznab">Newznab (usenet)</option>
                <option value="torznab">Torznab (torrents)</option>
              </select>
            </label>
            <label>
              URL
              <input
                placeholder="https://indexer.example"
                value={indexer.baseUrl}
                onChange={(e) => setIndexer({ ...indexer, baseUrl: e.target.value })}
              />
            </label>
            <label>
              API key
              <input value={indexer.apiKey} onChange={(e) => setIndexer({ ...indexer, apiKey: e.target.value })} />
            </label>
            <div className="settings-actions">
              <button className="toggle" disabled={busy || !indexerValid} onClick={testIndexer}>
                Test
              </button>
              <button disabled={busy || !indexerValid} onClick={addIndexer}>
                Add indexer
              </button>
              {indexersAdded > 0 && <span className="notice ok">{indexersAdded} added</span>}
            </div>
          </div>
        </>
      )}

      {step === 4 && (
        <>
          <h2>Where downloads go</h2>
          <p className="muted">
            <strong>qBittorrent</strong> handles torrents, <strong>SABnzbd</strong>{" "}
            handles usenet — debrid bridges speaking either API work too. Add
            what you have; the other can come later.
          </p>
          <div className="settings-form">
            <label>
              Name
              <input value={client.name} onChange={(e) => setClient({ ...client, name: e.target.value })} />
            </label>
            <label>
              Type
              <select
                value={client.type}
                onChange={(e) => setClient({ ...client, type: e.target.value as DownloadClient["type"] })}
              >
                <option value="qbittorrent">qBittorrent (torrents)</option>
                <option value="sabnzbd">SABnzbd (usenet)</option>
              </select>
            </label>
            <label>
              Host
              <input
                placeholder="http://localhost:8080"
                value={client.host}
                onChange={(e) => setClient({ ...client, host: e.target.value })}
              />
            </label>
            {client.type === "qbittorrent" ? (
              <>
                <label>
                  Username
                  <input value={client.username} onChange={(e) => setClient({ ...client, username: e.target.value })} />
                </label>
                <label>
                  Password
                  <input
                    type="password"
                    value={client.password}
                    onChange={(e) => setClient({ ...client, password: e.target.value })}
                  />
                </label>
              </>
            ) : (
              <label>
                API key
                <input
                  placeholder="Optional for keyless SABnzbd-compatible endpoints"
                  value={client.apiKey}
                  onChange={(e) => setClient({ ...client, apiKey: e.target.value })}
                />
              </label>
            )}
            <div className="settings-actions">
              <button className="toggle" disabled={busy || !clientValid} onClick={testClient}>
                Test
              </button>
              <button disabled={busy || !clientValid} onClick={addClient}>
                Add client
              </button>
              {clientsAdded > 0 && <span className="notice ok">{clientsAdded} added</span>}
            </div>
          </div>
        </>
      )}

      {step === 5 && (
        <>
          <h2>You're all set 🎉</h2>
          <p className="muted">
            {folders.length > 0
              ? `${folders.length} library folder${folders.length === 1 ? "" : "s"}`
              : "No library folders yet"}
            {" · "}
            {token.trim() ? "metadata connected" : "no metadata token"}
            {" · "}
            {indexersAdded} indexer{indexersAdded === 1 ? "" : "s"}
            {" · "}
            {clientsAdded} download client{clientsAdded === 1 ? "" : "s"}
          </p>
          <p className="muted">
            Everything here — and much more — lives in <strong>Settings</strong>.
            Add an author or series from a library page and LibriNode takes it
            from there: search, grab, download, import, organize.
          </p>
          <div className="settings-actions">
            <button onClick={onDone}>Enter LibriNode</button>
          </div>
        </>
      )}

      {step > 0 && step < 5 && (
        <div className="wizard-nav">
          <button className="toggle" disabled={busy || step === 1} onClick={back}>
            ← Back
          </button>
          {notice && step !== 2 && (
            <span className={notice.startsWith("✗") ? "notice bad" : "notice ok"}>{notice}</span>
          )}
          <button className="toggle wizard-skip" disabled={busy} onClick={next}>
            {step === 1 && folders.length > 0 ? "Continue →" : step === 3 && indexersAdded > 0 ? "Continue →" : step === 4 && clientsAdded > 0 ? "Continue →" : "Skip for now →"}
          </button>
        </div>
      )}
    </section>
  );
}

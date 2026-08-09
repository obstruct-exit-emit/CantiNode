package api

import (
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

var startTime = time.Now()

func (s *server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// localIPs lists the machine's non-loopback IPv4 addresses — what a user
// puts in another device's browser to reach LibriNode on the LAN.
func localIPs() []string {
	ips := []string{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue
			}
			if ip4 := ipNet.IP.To4(); ip4 != nil {
				ips = append(ips, ip4.String())
			}
		}
	}
	return ips
}

func (s *server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"appName": "LibriNode",
		// Prowlarr's Readarr application sync parses "version" as a dotted
		// .NET Version and enforces a minimum, so this reports a
		// Readarr-compatible number; LibriNode's real version is appVersion.
		"version":     "0.4.18.2805",
		"appVersion":  s.version,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"uptime":      time.Since(startTime).Round(time.Second).String(),
		"dataDir":     s.cfg.DataDir(),
		"startTime":   startTime.UTC().Format(time.RFC3339),
		"ipAddresses": localIPs(),
		"port":        s.cfg.Port,
	}
	// Prowlarr's Readarr proxy reads several more status fields and can
	// dereference them; provide the full Readarr shape so none are null.
	if isProwlarr(r) {
		readarrStatus := map[string]any{
			"instanceName":           "Readarr",
			"buildTime":              startTime.UTC().Format(time.RFC3339),
			"isDebug":                false,
			"isProduction":           true,
			"isAdmin":                false,
			"isUserInteractive":      false,
			"startupPath":            s.cfg.DataDir(),
			"appData":                s.cfg.DataDir(),
			"osName":                 runtime.GOOS,
			"osVersion":              "",
			"isNetCore":              true,
			"isLinux":                runtime.GOOS == "linux",
			"isOsx":                  runtime.GOOS == "darwin",
			"isWindows":              runtime.GOOS == "windows",
			"isDocker":               false,
			"mode":                   "console",
			"branch":                 "master",
			"authentication":         "none",
			"sqliteVersion":          "3.0.0",
			"migrationVersion":       1,
			"urlBase":                "",
			"runtimeVersion":         "6.0.0",
			"runtimeName":            "netCore",
			"packageVersion":         s.version,
			"packageAuthor":          "LibriNode",
			"packageUpdateMechanism": "builtIn",
			"databaseVersion":        "3.0.0",
			"databaseType":           "sqLite",
		}
		for k, v := range readarrStatus {
			resp[k] = v
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleIndex serves the embedded web UI: real files directly, anything else
// falls back to index.html so client-side routes work. Without an embedded
// build (backend-only compile) it serves a plain status page.
func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	// Unknown API routes must 404 as JSON, never fall back to the SPA.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "unknown API route")
		return
	}
	if s.webFS != nil {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && path != "index.html" {
			if _, err := fs.Stat(s.webFS, path); err == nil {
				// Vite emits content-hashed asset filenames (index-ABC123.js), so
				// a changed build is a changed URL — the bytes at one URL never
				// change and can be cached forever.
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.webFS, path)
				return
			}
		}
		// index.html (and the SPA fallback) references those hashed assets, so it
		// must never be cached — otherwise a deploy's new bundle is never picked
		// up and the browser keeps loading the old one.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		http.ServeFileFS(w, r, s.webFS, "index.html")
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html>
<title>LibriNode</title>
<style>body{font-family:system-ui;display:grid;place-items:center;min-height:90vh;background:#14141b;color:#e8e6e3}main{text-align:center}h1{font-size:2.5rem}p{color:#9a97a3}code{background:#22222c;padding:.2em .5em;border-radius:4px}</style>
<main>
  <h1>&#128396;&#65039; LibriNode</h1>
  <p>The written-media automation server is running.</p>
  <p>This build has no web UI embedded &mdash; run <code>npm run build</code> in <code>web/</code> and rebuild the binary. The API is fully available: try <code>GET /api/v1/system/status</code> with your API key.</p>
</main>`))
}

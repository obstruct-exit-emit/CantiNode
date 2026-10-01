package api

import (
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var startTime = time.Now()

func (s *server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// localIPs lists the machine's non-loopback IPv4 addresses — what a user
// puts in another device's browser to reach CantiNode on the LAN.
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
	writeJSON(w, http.StatusOK, map[string]any{
		"appName":     "CantiNode",
		"appVersion":  s.version,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"uptime":      time.Since(startTime).Round(time.Second).String(),
		"dataDir":     s.cfg.DataDir(),
		"startTime":   startTime.UTC().Format(time.RFC3339),
		"ipAddresses": localIPs(),
		"port":        s.cfg.Port,
	})
}

// systemCommandRunner is exec.Command by default — swapped out in tests so
// an admin triggering "update"/"restart" never actually shells out to a
// real system command (or reboots the test machine) during `go test`.
var systemCommandRunner = exec.Command

// runSystemCommand runs command through a login shell (bash -lc) — the
// same way it would run if the admin typed it into the server's own
// console themselves, sourcing whatever profile/PATH entry makes a bare
// "update" (or any other deployment-specific alias/script) resolve to
// something real, which a bare exec.Command(command) alone wouldn't.
//
// Launched via `systemd-run --scope`, in its own transient scope unit
// outside CantiNode's own systemd cgroup, not as a direct child process —
// confirmed live as the actual root cause of a real outage: an "update"
// script that itself calls systemctl stop/restart on this very service is
// a child process of that service's own cgroup, so systemd's default
// KillMode=control-group kills the script itself the instant it reaches
// that line (collateral damage from stopping its own parent), cutting it
// off before it ever reaches whatever comes after — rebuilding, then
// starting the new binary. That leaves the unit cleanly stopped (not
// failed), which systemd has no reason to auto-restart, so nothing brings
// it back until a human notices and starts it by hand. A separate scope,
// managed directly by systemd (PID 1) rather than nested inside this
// service's own cgroup, survives cantinode.service being stopped out from
// under it, the same way any real "self-updating service" script needs
// to.
//
// Kicks it off and returns immediately rather than waiting for it to
// finish: the whole point of "update" or "reboot now" is to stop or
// replace this very process, so a response the caller is still waiting on
// can vanish along with the connection mid-command — the admin watches
// the console/log directly to see it actually happen, same as running it
// by hand.
//
// systemd-run's own transient-unit creation needs either root (bypasses
// polkit outright — true for every deployment confirmed working so far)
// or an explicit polkit rule granting org.freedesktop.systemd1.manage-units
// to CantiNode's own service account when it runs as a dedicated
// lower-privilege user (e.g. the systemd unit template's own "User=
// cantinode" convention) — confirmed live: that exact non-root setup fails
// with "Interactive authentication required" otherwise. A clean failure
// (cmd.Start() itself errors, logged and returned as a 500) either way,
// never a silent one.
func (s *server) runSystemCommand(w http.ResponseWriter, command string) {
	slog.Warn("api: admin triggered a system command", "command", command)
	cmd := systemCommandRunner("systemd-run", "--scope", "--collect", "--quiet", "--", "bash", "-lc", command)
	if err := cmd.Start(); err != nil {
		slog.Error("api: system command failed to start", "command", command, "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Warn("api: system command process ended", "command", command, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// handleSystemUpdate runs the host's own update command — config.
// SystemSettings.UpdateCmd() (default "update"), configurable per
// deployment at Settings → System rather than fixed in source, since what
// "update" means is entirely host-specific. Whatever it resolves to is
// run through a login shell, same as an admin would type into the
// server's own console to pull and apply a new build. Admin-only
// (requireAdmin, see router.go): this is arbitrary host-level execution,
// not scoped to CantiNode's own data at all.
func (s *server) handleSystemUpdate(w http.ResponseWriter, r *http.Request) {
	s.runSystemCommand(w, s.cfg.SystemSettings().UpdateCmd())
}

// handleSystemRestart runs config.SystemSettings.RestartCmd() (default
// "reboot now" — the host, not just this process), same configurability
// and reasoning as handleSystemUpdate's own doc comment: a deployment that
// wants the lighter-weight "just restart the service" behavior sets this
// explicitly instead. Admin-only, see handleSystemUpdate's own doc
// comment.
func (s *server) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	s.runSystemCommand(w, s.cfg.SystemSettings().RestartCmd())
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
<title>CantiNode</title>
<style>body{font-family:system-ui;display:grid;place-items:center;min-height:90vh;background:#14141b;color:#e8e6e3}main{text-align:center}h1{font-size:2.5rem}p{color:#9a97a3}code{background:#22222c;padding:.2em .5em;border-radius:4px}</style>
<main>
  <h1>&#127925; CantiNode</h1>
  <p>The music automation server is running.</p>
  <p>This build has no web UI embedded &mdash; run <code>npm run build</code> in <code>web/</code> and rebuild the binary. The API is fully available: try <code>GET /api/v1/system/status</code> with your API key.</p>
</main>`))
}

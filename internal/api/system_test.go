package api

import (
	"net/http"
	"os/exec"
	"testing"
)

// capturedCommand records what systemCommandRunner was last called with.
type capturedCommand struct {
	name string
	args []string
}

// fakeSystemCommand swaps systemCommandRunner for the duration of a test,
// recording the name/args it was called with and running a real but
// harmless command in its place (`true`) so Start/Wait still behave like a
// genuine subprocess — never the actual "update"/"reboot now" command,
// which would be meaningless (or actively dangerous) to run for real
// under `go test`.
func fakeSystemCommand(t *testing.T) *capturedCommand {
	t.Helper()
	got := &capturedCommand{}
	orig := systemCommandRunner
	systemCommandRunner = func(name string, arg ...string) *exec.Cmd {
		got.name, got.args = name, arg
		return exec.Command("true")
	}
	t.Cleanup(func() { systemCommandRunner = orig })
	return got
}

// TestSystemUpdateAndRestartRunTheExpectedCommand confirms each endpoint
// invokes exactly the command an admin typing it into the server's own
// console would run — "update" and "reboot now" respectively, via a login
// shell (bash -lc) so a deployment-specific alias/script/PATH entry
// resolves the same way it would interactively.
func TestSystemUpdateAndRestartRunTheExpectedCommand(t *testing.T) {
	a := newTestAPI(t)

	got := fakeSystemCommand(t)
	a.want(a.call("POST", "/api/v1/system/update", nil, nil), http.StatusAccepted)
	if got.name != "bash" || len(got.args) != 2 || got.args[0] != "-lc" || got.args[1] != "update" {
		t.Errorf("update command = %q %v, want bash -lc update", got.name, got.args)
	}

	got = fakeSystemCommand(t)
	a.want(a.call("POST", "/api/v1/system/restart", nil, nil), http.StatusAccepted)
	if got.name != "bash" || len(got.args) != 2 || got.args[0] != "-lc" || got.args[1] != "reboot now" {
		t.Errorf("restart command = %q %v, want bash -lc \"reboot now\"", got.name, got.args)
	}
}

// TestSystemUpdateAndRestartUseConfiguredCommand is the regression test
// for an altitude fix: the exact command each button runs used to be a
// hardcoded Go string literal, baking one deployment's own convention
// (an "update" shell alias, a full host "reboot now") into source for
// every CantiNode install — a different deployment (no such alias, a
// container where rebooting the host is wrong) had no way to change it
// short of editing Go source and rebuilding. Settings → System
// (config.SystemSettings) now makes this configurable; confirmed here
// that a saved override actually reaches the command execution, not just
// the settings round trip.
func TestSystemUpdateAndRestartUseConfiguredCommand(t *testing.T) {
	a := newTestAPI(t)

	var saved struct {
		UpdateCommand  string `json:"updateCommand"`
		RestartCommand string `json:"restartCommand"`
	}
	a.want(a.call("PUT", "/api/v1/settings/system",
		map[string]string{"updateCommand": "deploy.sh", "restartCommand": "systemctl reboot"},
		&saved), http.StatusOK)
	if saved.UpdateCommand != "deploy.sh" || saved.RestartCommand != "systemctl reboot" {
		t.Fatalf("saved settings = %+v, want the values just submitted", saved)
	}

	got := fakeSystemCommand(t)
	a.want(a.call("POST", "/api/v1/system/update", nil, nil), http.StatusAccepted)
	if got.name != "bash" || len(got.args) != 2 || got.args[1] != "deploy.sh" {
		t.Errorf("update command = %q %v, want bash -lc deploy.sh (the configured override)", got.name, got.args)
	}

	got = fakeSystemCommand(t)
	a.want(a.call("POST", "/api/v1/system/restart", nil, nil), http.StatusAccepted)
	if got.name != "bash" || len(got.args) != 2 || got.args[1] != "systemctl reboot" {
		t.Errorf("restart command = %q %v, want bash -lc \"systemctl reboot\" (the configured override)", got.name, got.args)
	}
}

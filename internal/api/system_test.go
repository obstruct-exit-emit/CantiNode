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

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCreateRestoreDelete(t *testing.T) {
	a := newTestAPI(t, nil)

	var status struct {
		DataDir string `json:"dataDir"`
	}
	a.want(a.call("GET", "/api/v1/system/status", nil, &status), http.StatusOK)

	// Create: zip with a database snapshot and config.yaml.
	var created backupInfo
	a.want(a.call("POST", "/api/v1/backup", nil, &created), http.StatusCreated)
	if !backupName.MatchString(created.Name) || created.Size == 0 {
		t.Fatalf("created = %+v", created)
	}

	var list []backupInfo
	a.want(a.call("GET", "/api/v1/backup", nil, &list), http.StatusOK)
	if len(list) != 1 || list[0].Name != created.Name {
		t.Fatalf("list = %+v", list)
	}

	// Restore stages *.restore files; the swap happens at startup.
	var restored struct {
		Staged int `json:"staged"`
	}
	a.want(a.call("POST", "/api/v1/backup/"+created.Name+"/restore", nil, &restored), http.StatusOK)
	if restored.Staged != 2 {
		t.Fatalf("staged = %d, want 2", restored.Staged)
	}
	for _, f := range []string{"librinode.db.restore", "config.yaml.restore"} {
		if _, err := os.Stat(filepath.Join(status.DataDir, f)); err != nil {
			t.Errorf("%s not staged: %v", f, err)
		}
	}

	// Path traversal shapes are rejected by the name pattern.
	a.want(a.call("DELETE", "/api/v1/backup/..%2Fconfig.yaml", nil, nil), http.StatusBadRequest)

	a.want(a.call("DELETE", "/api/v1/backup/"+created.Name, nil, nil), http.StatusNoContent)
	a.want(a.call("GET", "/api/v1/backup", nil, &list), http.StatusOK)
	if len(list) != 0 {
		t.Fatalf("after delete, list = %+v", list)
	}
}

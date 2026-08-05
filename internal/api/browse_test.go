package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBrowseDirectoriesListsSubdirectories(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "b-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "not-a-dir.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "GET", "/api/v1/browse-directories?path="+url.QueryEscape(root), apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp browseDirectoriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Directories) != 2 {
		t.Fatalf("directories = %+v, want 2 (files must be excluded)", resp.Directories)
	}
	if resp.Directories[0].Name != "a-folder" || resp.Directories[1].Name != "b-folder" {
		t.Errorf("directories = %+v, want sorted a-folder, b-folder", resp.Directories)
	}
	if resp.Parent == nil {
		t.Error("Parent should be set for a non-root path")
	}
}

func TestBrowseDirectoriesEmptyPathListsRoots(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	rec := doRequest(t, s, "GET", "/api/v1/browse-directories", apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp browseDirectoriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Parent != nil {
		t.Errorf("Parent = %v, want nil at the top level", resp.Parent)
	}
	if len(resp.Directories) == 0 {
		t.Error("expected at least one root directory (drive letter or /)")
	}
	if runtime.GOOS != "windows" {
		if resp.Directories[0].Path != "/" {
			t.Errorf("Directories = %+v, want / on a non-Windows OS", resp.Directories)
		}
	}
}

func TestBrowseDirectoriesRejectsNonexistentPath(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	rec := doRequest(t, s, "GET", "/api/v1/browse-directories?path="+url.QueryEscape(filepath.Join(t.TempDir(), "does-not-exist")), apiKey, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestBrowseDirectoriesRejectsFilePath(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	filePath := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "GET", "/api/v1/browse-directories?path="+url.QueryEscape(filePath), apiKey, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

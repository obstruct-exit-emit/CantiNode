package database

import "testing"

func TestRootFolderCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	rf, err := db.CreateRootFolder(ctx, "/music")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if rf.ID == 0 {
		t.Error("expected nonzero ID")
	}

	got, err := db.GetRootFolder(ctx, rf.ID)
	if err != nil {
		t.Fatalf("GetRootFolder: %v", err)
	}
	if got.Path != "/music" {
		t.Errorf("Path = %q, want /music", got.Path)
	}

	list, err := db.ListRootFolders(ctx)
	if err != nil {
		t.Fatalf("ListRootFolders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}

	if err := db.DeleteRootFolder(ctx, rf.ID); err != nil {
		t.Fatalf("DeleteRootFolder: %v", err)
	}
	if _, err := db.GetRootFolder(ctx, rf.ID); err != ErrNotFound {
		t.Errorf("GetRootFolder after delete: err = %v, want ErrNotFound", err)
	}
}

// TestListRootFoldersEmptyIsNotNil guards against a real bug: a nil slice
// (Go's zero value, what "var out []RootFolder" produces when a query
// returns zero rows) JSON-marshals to `null`, not `[]` — which crashed
// the web UI's artists.length-style checks on a fresh install with an
// empty library. ListRootFolders (and every other List* in this package)
// must always return a non-nil, possibly-empty slice.
func TestListRootFoldersEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	list, err := db.ListRootFolders(t.Context())
	if err != nil {
		t.Fatalf("ListRootFolders: %v", err)
	}
	if list == nil {
		t.Error("ListRootFolders returned nil for an empty result, want a non-nil empty slice (marshals to `null` instead of `[]`)")
	}
}

func TestCreateRootFolderDuplicatePathFails(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	if _, err := db.CreateRootFolder(ctx, "/music"); err != nil {
		t.Fatalf("first CreateRootFolder: %v", err)
	}
	if _, err := db.CreateRootFolder(ctx, "/music"); err == nil {
		t.Error("expected error inserting duplicate path")
	}
}

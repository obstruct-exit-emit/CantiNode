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

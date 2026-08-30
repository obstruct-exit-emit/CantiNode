package importlist

import (
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestAddGetListDelete(t *testing.T) {
	s := newTestStore(t)

	l := &ImportList{Name: "NOW That's What I Call Music", Type: TypeMusicBrainzSeries, SeriesMBID: "series-mbid", Enabled: true}
	if err := s.Add(l); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if l.ID == 0 {
		t.Error("expected nonzero ID")
	}
	if l.AddedAt == "" {
		t.Error("expected AddedAt to be set")
	}

	got, err := s.Get(l.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != l.Name || got.SeriesMBID != "series-mbid" {
		t.Errorf("got = %+v", got)
	}

	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1", len(all))
	}

	if err := s.Delete(l.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(l.ID); err != ErrNotFound {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.Update(&ImportList{ID: 999, Name: "x", Type: TypeList})
	if err != ErrNotFound {
		t.Errorf("Update on missing row = %v, want ErrNotFound", err)
	}
}

func TestSetSyncResult(t *testing.T) {
	s := newTestStore(t)
	l := &ImportList{Name: "My List", Type: TypeList, ListText: "Boards of Canada", Enabled: true}
	if err := s.Add(l); err != nil {
		t.Fatal(err)
	}

	if err := s.SetSyncResult(l.ID, "2026-01-01T00:00:00Z", "network unreachable"); err != nil {
		t.Fatalf("SetSyncResult: %v", err)
	}
	got, err := s.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncedAt != "2026-01-01T00:00:00Z" || got.LastSyncError != "network unreachable" {
		t.Errorf("got = %+v", got)
	}

	if err := s.SetSyncResult(l.ID, "2026-01-02T00:00:00Z", ""); err != nil {
		t.Fatalf("SetSyncResult: %v", err)
	}
	got, err = s.Get(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncError != "" {
		t.Errorf("LastSyncError = %q, want cleared on a successful sync", got.LastSyncError)
	}
}

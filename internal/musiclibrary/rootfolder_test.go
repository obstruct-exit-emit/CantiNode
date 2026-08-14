package musiclibrary

import (
	"errors"
	"testing"
)

func TestRenameRootFolder(t *testing.T) {
	s := newTestStore(t)
	id := testMusicRoot(t, s)

	if err := s.RenameRootFolder(id, "Archive Drive"); err != nil {
		t.Fatalf("RenameRootFolder: %v", err)
	}
	rf, err := s.GetRootFolder(id)
	if err != nil {
		t.Fatalf("GetRootFolder: %v", err)
	}
	if rf.Name != "Archive Drive" {
		t.Errorf("Name = %q, want %q", rf.Name, "Archive Drive")
	}
}

func TestRenameRootFolderNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.RenameRootFolder(999, "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestSetDefaultRootFolderEnsuresExactlyOne confirms setting a new
// default always clears whichever folder previously held it — never two
// folders marked default at once.
func TestSetDefaultRootFolderEnsuresExactlyOne(t *testing.T) {
	s := newTestStore(t)
	id1 := testMusicRoot(t, s)
	id2 := testMusicRoot(t, s)

	if err := s.SetDefaultRootFolder(id1); err != nil {
		t.Fatalf("SetDefaultRootFolder(id1): %v", err)
	}
	def, err := s.DefaultRootFolder()
	if err != nil || def.ID != id1 {
		t.Fatalf("DefaultRootFolder = %+v, %v, want id1", def, err)
	}

	if err := s.SetDefaultRootFolder(id2); err != nil {
		t.Fatalf("SetDefaultRootFolder(id2): %v", err)
	}
	def, err = s.DefaultRootFolder()
	if err != nil || def.ID != id2 {
		t.Fatalf("DefaultRootFolder after switching = %+v, %v, want id2", def, err)
	}

	folders, err := s.ListRootFolders()
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, f := range folders {
		if f.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("folders with IsDefault = %d, want exactly 1: %+v", defaults, folders)
	}
}

func TestSetDefaultRootFolderNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetDefaultRootFolder(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDefaultRootFolderNotFoundWhenNoneConfigured(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DefaultRootFolder(); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// seedArtistFileOn creates (or reuses) an owned, matched track file for
// artistID directly under rootFolderID — the minimum needed for
// ArtistRootFolder to see it.
func seedArtistFileOn(t *testing.T, s *Store, artistID, rootFolderID int64, uniq string) {
	t.Helper()
	album, err := s.GetOrCreateAlbum(artistID, "al-"+uniq, "rg-"+uniq, "Album "+uniq, "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.GetOrCreateTrack(album.ID, "t-"+uniq, "Track "+uniq, 1, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}
	tf, err := s.UpsertTrackFileByPath(rootFolderID, "/music/"+uniq+".flac", 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrackFileMatch(tf.ID, &track.ID, StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
}

// TestArtistRootFolderPrefersMostFiles seeds an artist with more files on
// one root folder than another and confirms ArtistRootFolder picks the
// one holding the most — the signal internal/importer's targetRootFolder
// uses to keep a new grab joining an artist's existing discography
// instead of splitting it across folders.
func TestArtistRootFolderPrefersMostFiles(t *testing.T) {
	s := newTestStore(t)
	rootA := testMusicRoot(t, s)
	rootB := testMusicRoot(t, s)

	artist, err := s.GetOrCreateArtist("a-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	seedArtistFileOn(t, s, artist.ID, rootA, "a1")
	seedArtistFileOn(t, s, artist.ID, rootA, "a2")
	seedArtistFileOn(t, s, artist.ID, rootB, "b1")

	rf, err := s.ArtistRootFolder(artist.ID)
	if err != nil {
		t.Fatalf("ArtistRootFolder: %v", err)
	}
	if rf.ID != rootA {
		t.Errorf("ArtistRootFolder = %d, want rootA (%d) — has 2 files vs rootB's 1", rf.ID, rootA)
	}
}

func TestArtistRootFolderNotFoundWhenArtistOwnsNothing(t *testing.T) {
	s := newTestStore(t)
	artist, err := s.GetOrCreateArtist("a-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArtistRootFolder(artist.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

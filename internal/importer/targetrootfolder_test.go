package importer

import (
	"testing"

	"github.com/cantinode/cantinode/internal/download"
)

// TestTargetRootFolderPrefersArtistExistingFolder confirms a new grab for
// an artist that already owns files somewhere goes to that same root
// folder, even when a DIFFERENT root folder is the instance-wide
// default — an artist's discography should never split across folders
// just because of where a later grab happened to land.
func TestTargetRootFolderPrefersArtistExistingFolder(t *testing.T) {
	sab, _ := mockSab(t, "", "Completed")
	svc, _, musicStore, existingRootPath := setup(t, sab)

	existingRoots, err := musicStore.ListRootFolders()
	if err != nil || len(existingRoots) != 1 {
		t.Fatalf("existing roots = %+v, %v, want exactly 1 from setup()", existingRoots, err)
	}
	artistRoot := existingRoots[0]
	if artistRoot.Path != existingRootPath {
		t.Fatalf("artistRoot.Path = %q, want %q", artistRoot.Path, existingRootPath)
	}

	defaultRoot, err := musicStore.CreateRootFolder(t.TempDir(), "Default")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetDefaultRootFolder(defaultRoot.ID); err != nil {
		t.Fatal(err)
	}

	artist, err := musicStore.GetOrCreateArtist("a-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Existing Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := musicStore.GetOrCreateTrack(album.ID, "t-mbid", "Track", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tf, err := musicStore.UpsertTrackFileByPath(artistRoot.ID, artistRoot.Path+"/existing.flac", 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(tf.ID, &track.ID, "matched", 1.0); err != nil {
		t.Fatal(err)
	}

	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-new", "New Album", "Album", "2021")
	if err != nil {
		t.Fatal(err)
	}

	got, ok := svc.targetRootFolder(download.GrabRecord{WantedAlbumID: wanted.ID})
	if !ok {
		t.Fatal("targetRootFolder returned ok=false")
	}
	if got.ID != artistRoot.ID {
		t.Errorf("targetRootFolder = %+v, want the artist's existing root folder (id %d), not the default (id %d)",
			got, artistRoot.ID, defaultRoot.ID)
	}
}

// TestTargetRootFolderFallsBackToDefaultForNewArtist confirms a grab for
// an artist with no existing files anywhere lands on whichever root
// folder is marked default.
func TestTargetRootFolderFallsBackToDefaultForNewArtist(t *testing.T) {
	sab, _ := mockSab(t, "", "Completed")
	svc, _, musicStore, _ := setup(t, sab)

	defaultRoot, err := musicStore.CreateRootFolder(t.TempDir(), "Default")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetDefaultRootFolder(defaultRoot.ID); err != nil {
		t.Fatal(err)
	}

	artist, err := musicStore.GetOrCreateArtist("a-new", "Brand New Artist", "Brand New Artist")
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-new", "First Album", "Album", "2021")
	if err != nil {
		t.Fatal(err)
	}

	got, ok := svc.targetRootFolder(download.GrabRecord{WantedAlbumID: wanted.ID})
	if !ok {
		t.Fatal("targetRootFolder returned ok=false")
	}
	if got.ID != defaultRoot.ID {
		t.Errorf("targetRootFolder = %+v, want the default root folder (id %d)", got, defaultRoot.ID)
	}
}

// TestTargetRootFolderFallsBackToFirstWhenNoDefaultSet covers the
// last-resort path: nothing is marked default at all (shouldn't happen
// once any root folder exists, since the API always marks the first one
// added, but must never leave an import with nowhere to go).
func TestTargetRootFolderFallsBackToFirstWhenNoDefaultSet(t *testing.T) {
	sab, _ := mockSab(t, "", "Completed")
	svc, _, musicStore, _ := setup(t, sab)

	roots, err := musicStore.ListRootFolders()
	if err != nil || len(roots) != 1 {
		t.Fatalf("roots = %+v, %v, want exactly 1", roots, err)
	}

	got, ok := svc.targetRootFolder(download.GrabRecord{})
	if !ok {
		t.Fatal("targetRootFolder returned ok=false")
	}
	if got.ID != roots[0].ID {
		t.Errorf("targetRootFolder = %+v, want the only configured root folder", got)
	}
}

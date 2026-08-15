package musicscanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

func TestWriteTagsEmbedsMatchedMetadata(t *testing.T) {
	s, rf := setupOrganizeScanner(t) // no MusicBrainz client needed — WriteTags never calls it

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(rf.Path, "song.mp3")
	if err := os.WriteFile(path, []byte("fake mp3 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTags(tf.ID); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Title != "Alpha and Omega" || got.Artist != "Boards of Canada" || got.Album != "Geogaddi" {
		t.Errorf("got Title=%q Artist=%q Album=%q", got.Title, got.Artist, got.Album)
	}
	if got.MusicBrainzRecordingID != "t-mbid" {
		t.Errorf("MusicBrainzRecordingID = %q, want t-mbid", got.MusicBrainzRecordingID)
	}
}

// TestWriteTagsUsesPerTrackArtistCreditForVariousArtists is the
// regression test for the tagwriter gap: on a Various Artists
// compilation, ARTIST must reflect the track's own real performer
// (track.ArtistCredit), not the "Various Artists" artist row every track
// on the release files under — ALBUMARTIST alone carries that compilation
// identity.
func TestWriteTagsUsesPerTrackArtistCreditForVariousArtists(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("va-mbid", "Various Artists", "Various Artists")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Now That's What I Call Music", "1998", "Compilation")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "In the Air Tonight", 1, 1, 200000, "Phil Collins")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(rf.Path, "song.mp3")
	if err := os.WriteFile(path, []byte("fake mp3 audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTags(tf.ID); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Artist != "Phil Collins" {
		t.Errorf("Artist = %q, want the track's own credit Phil Collins, not the release's Various Artists", got.Artist)
	}
	if got.AlbumArtist != "Various Artists" {
		t.Errorf("AlbumArtist = %q, want Various Artists (the compilation identity)", got.AlbumArtist)
	}
}

func TestWriteTagsRequiresMatch(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	path := filepath.Join(rf.Path, "song.mp3")
	os.WriteFile(path, []byte("x"), 0o644)
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTags(tf.ID); err == nil {
		t.Error("expected an error writing tags for an unmatched file")
	}
}

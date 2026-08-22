package musicscanner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/coverart"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
	"github.com/cantinode/cantinode/internal/tagwriter"
	taglibpkg "go.senan.xyz/taglib"
)

// setupOrganizeScannerWithCoverart is setupOrganizeScanner plus a real
// coverart.Client pointed at a fake Cover Art Archive server — the tests
// below need this to verify WriteTags actually embeds a cover, not just
// that it leaves CoverImage blank the way every other test (coverart nil)
// already covers implicitly.
func setupOrganizeScannerWithCoverart(t *testing.T, caaHandler http.HandlerFunc) (*Scanner, musiclibrary.RootFolder) {
	t.Helper()
	caa := httptest.NewServer(caaHandler)
	t.Cleanup(caa.Close)

	sqlDB, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db := musiclibrary.NewStore(sqlDB)

	rootDir := t.TempDir()
	res, err := sqlDB.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, rootDir)
	if err != nil {
		t.Fatal(err)
	}
	rfID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	rf, err := db.GetRootFolder(rfID)
	if err != nil {
		t.Fatal(err)
	}

	coverartClient := coverart.NewClientWithBaseURL(t.TempDir(), "cantinode-test/0.1", caa.URL, nil)
	s := New(db, nil, coverartClient, nil, "{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}", 0.75, false, tagwriter.AllEnabled)
	return s, *rf
}

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
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "", "", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
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
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "In the Air Tonight", 1, 1, 200000, "Phil Collins", "", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
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

// TestWriteTagsUsesPerTrackArtistMBIDForVariousArtists is the regression
// test for a real live bug: on a Various Artists compilation, the ARTIST
// tag correctly named the track's own real performer (see
// TestWriteTagsUsesPerTrackArtistCreditForVariousArtists above), but the
// embedded MusicBrainz Artist Id right next to it was always the release's
// filing artist ("Various Artists") regardless — the name and the ID
// tag disagreeing about whose identity the frame even carried. A separate
// "MusicBrainz Album Artist Id" tag now always carries the filing artist,
// freeing MusicBrainz Artist Id to correctly hold the track's own real
// performer's own ID.
func TestWriteTagsUsesPerTrackArtistMBIDForVariousArtists(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("va-mbid", "Various Artists", "Various Artists")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Now That's What I Call Music", "1998", "Compilation")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "In the Air Tonight", 1, 1, 200000, "Phil Collins", "phil-collins-mbid", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	if vals := got[taglibpkg.MusicBrainzArtistID]; len(vals) == 0 || vals[0] != "phil-collins-mbid" {
		t.Errorf("MusicBrainz Artist Id = %v, want [phil-collins-mbid] (the track's own real performer, not the filing artist)", vals)
	}
	if vals := got[taglibpkg.MusicBrainzAlbumArtistID]; len(vals) == 0 || vals[0] != "va-mbid" {
		t.Errorf("MusicBrainz Album Artist Id = %v, want [va-mbid] (the release's filing artist)", vals)
	}
}

// TestWriteTagsEmbedsGenreReleaseTypeSortNamesAndTotals covers the rest of
// the "are we writing everything MusicBrainz supplies" gap: genre (from
// the artist's own cached genres), release type, artist/album-artist sort
// names, and track/disc totals (computed from the album's own tracklist,
// not stored anywhere) — all sourced from data already cached for every
// write, no new fetches.
func TestWriteTagsEmbedsGenreReleaseTypeSortNamesAndTotals(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetArtistMusicBrainzMetadata(artist.ID, []string{"IDM", "Electronic"}, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track1, err := s.db.GetOrCreateTrack(album.ID, "t1-mbid", "Alpha and Omega", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.GetOrCreateTrack(album.ID, "t2-mbid", "Music Is Math", 2, 1, 200000, "", "", ""); err != nil {
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
	if err := s.db.SetTrackFileMatch(tf.ID, &track1.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	check := func(key string, want string) {
		t.Helper()
		vals := got[key]
		if len(vals) == 0 || vals[0] != want {
			t.Errorf("%s = %v, want [%s]", key, vals, want)
		}
	}
	check(taglibpkg.Genre, "IDM; Electronic")
	check(taglibpkg.ReleaseType, "Album")
	check(taglibpkg.ArtistSort, "Boards of Canada")
	check(taglibpkg.AlbumArtistSort, "Boards of Canada")
	check(taglibpkg.Date, "2002-02-04")
	check("TRACKTOTAL", "2") // two tracks on this album's one disc
	check("DISCTOTAL", "1")
}

// TestWriteTagsEmbedsReleaseCountryStatusAndMedia covers the last of the
// "are we writing everything MusicBrainz supplies" gaps: release country,
// status, and media format, sourced from the same release_group_versions
// cache that already backs the album page's own edition/pressing picker —
// no new fetch, just reading data that was already sitting there once
// that cache is populated for the exact release matched (not just any
// version of the release group).
func TestWriteTagsEmbedsReleaseCountryStatusAndMedia(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.ReplaceReleaseGroupVersions("rg-mbid", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-mbid", ReleaseMBID: "al-mbid", Title: "Geogaddi", Country: "GB", Status: "official", MediaSummary: "2×CD", IsRepresentative: true},
	}); err != nil {
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	check := func(key, want string) {
		t.Helper()
		vals := got[key]
		if len(vals) == 0 || vals[0] != want {
			t.Errorf("%s = %v, want [%s]", key, vals, want)
		}
	}
	check(taglibpkg.ReleaseCountry, "GB")
	check(taglibpkg.ReleaseStatus, "official")
	check(taglibpkg.Media, "2×CD")
}

// TestWriteTagsForAlbumSkipsUnmatchedAndWritesTheRest is the regression
// test for the album page's new bulk "Write tags" action (replacing the
// per-file button): every matched file in the album gets its tags
// written, an unmatched file is silently skipped (nothing to write, not
// an error), and the returned count reflects only the files actually
// written.
func TestWriteTagsForAlbumSkipsUnmatchedAndWritesTheRest(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track1, err := s.db.GetOrCreateTrack(album.ID, "t1-mbid", "Alpha and Omega", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	track2, err := s.db.GetOrCreateTrack(album.ID, "t2-mbid", "Music Is Math", 2, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	path1 := filepath.Join(rf.Path, "01.mp3")
	os.WriteFile(path1, []byte("fake mp3 audio"), 0o644)
	tf1, err := s.db.UpsertTrackFileByPath(rf.ID, path1, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf1.ID, &track1.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	path2 := filepath.Join(rf.Path, "02.mp3")
	os.WriteFile(path2, []byte("fake mp3 audio"), 0o644)
	tf2, err := s.db.UpsertTrackFileByPath(rf.ID, path2, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf2.ID, &track2.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	// A third, unmatched file in the same album — SetTrackFileMatch never
	// called, so it stays StatusUnmatched.
	path3 := filepath.Join(rf.Path, "03.mp3")
	os.WriteFile(path3, []byte("fake mp3 audio"), 0o644)
	if _, err := s.db.UpsertTrackFileByPath(rf.ID, path3, 1, "mp3", 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}

	written, errs, err := s.WriteTagsForAlbum(context.Background(), album.ID, false)
	if err != nil {
		t.Fatalf("WriteTagsForAlbum: %v", err)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2 (the unmatched file should be skipped, not counted or errored)", written)
	}
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}

	got1, err := tagreader.Read(path1)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Title != "Alpha and Omega" {
		t.Errorf("path1 Title = %q, want Alpha and Omega", got1.Title)
	}
	got2, err := tagreader.Read(path2)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Title != "Music Is Math" {
		t.Errorf("path2 Title = %q, want Music Is Math", got2.Title)
	}
}

// TestWriteTagsForArtistCoversEveryAlbum proves the artist-scoped bulk
// action spans every album the artist owns, not just one.
func TestWriteTagsForArtistCoversEveryAlbum(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album1, err := s.db.GetOrCreateAlbum(artist.ID, "al1-mbid", "rg1-mbid", "Geogaddi", "2002", "Album")
	if err != nil {
		t.Fatal(err)
	}
	album2, err := s.db.GetOrCreateAlbum(artist.ID, "al2-mbid", "rg2-mbid", "Music Has the Right to Children", "1998", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track1, err := s.db.GetOrCreateTrack(album1.ID, "t1-mbid", "Alpha and Omega", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	track2, err := s.db.GetOrCreateTrack(album2.ID, "t2-mbid", "Roygbiv", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	path1 := filepath.Join(rf.Path, "01.mp3")
	os.WriteFile(path1, []byte("fake mp3 audio"), 0o644)
	tf1, err := s.db.UpsertTrackFileByPath(rf.ID, path1, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf1.ID, &track1.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	path2 := filepath.Join(rf.Path, "02.mp3")
	os.WriteFile(path2, []byte("fake mp3 audio"), 0o644)
	tf2, err := s.db.UpsertTrackFileByPath(rf.ID, path2, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf2.ID, &track2.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	written, errs, err := s.WriteTagsForArtist(context.Background(), artist.ID, false)
	if err != nil {
		t.Fatalf("WriteTagsForArtist: %v", err)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2 (one file from each of the artist's two albums)", written)
	}
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none", errs)
	}
}

// TestWriteTagsEmbedsAlbumCoverArt confirms WriteTags actually fetches
// and embeds a real cover image via a stand-in coverart.Client, not just
// that it tolerates a nil one (every other test in this file covers that
// implicitly).
func TestWriteTagsEmbedsAlbumCoverArt(t *testing.T) {
	fakeJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xD9}
	s, rf := setupOrganizeScannerWithCoverart(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(fakeJPEG)
	})

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "", "", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if len(got) != len(fakeJPEG) {
		t.Errorf("embedded image = %d bytes, want %d", len(got), len(fakeJPEG))
	}
}

// TestWriteTagsForAlbumFetchesCoverOncePerAlbum confirms WriteTagsForAlbum
// caches the cover fetch across a whole album's files, not once per
// track file — coverart.Client.GetFrontCover already disk-caches, but a
// bulk write across several tracks shouldn't repeat even that many
// times.
func TestWriteTagsForAlbumFetchesCoverOncePerAlbum(t *testing.T) {
	requests := 0
	fakeJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xD9}
	s, rf := setupOrganizeScannerWithCoverart(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(fakeJPEG)
	})

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track1, err := s.db.GetOrCreateTrack(album.ID, "t1-mbid", "Alpha and Omega", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	track2, err := s.db.GetOrCreateTrack(album.ID, "t2-mbid", "Music Is Math", 2, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	path1 := filepath.Join(rf.Path, "01.mp3")
	os.WriteFile(path1, []byte("fake mp3 audio"), 0o644)
	tf1, err := s.db.UpsertTrackFileByPath(rf.ID, path1, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf1.ID, &track1.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
	path2 := filepath.Join(rf.Path, "02.mp3")
	os.WriteFile(path2, []byte("fake mp3 audio"), 0o644)
	tf2, err := s.db.UpsertTrackFileByPath(rf.ID, path2, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf2.ID, &track2.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	written, errs, err := s.WriteTagsForAlbum(context.Background(), album.ID, false)
	if err != nil {
		t.Fatalf("WriteTagsForAlbum: %v", err)
	}
	if written != 2 || len(errs) != 0 {
		t.Fatalf("written = %d, errs = %v", written, errs)
	}
	if requests != 1 {
		t.Errorf("cover art server received %d requests, want exactly 1 for both tracks combined", requests)
	}
}

// TestWriteTagsEmbedsMood confirms Album.Mood (TheAudioDB's own field,
// cached alongside the album's description) gets written.
func TestWriteTagsEmbedsMood(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetAlbumDescription(album.ID, "A dark, hypnotic record.", "Trippy", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "", "", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	if vals := got[taglibpkg.Mood]; len(vals) == 0 || vals[0] != "Trippy" {
		t.Errorf("Mood = %v, want [Trippy]", vals)
	}
}

// TestWriteTagsRespectsDisabledFieldToggle is the end-to-end regression
// test for the Settings → Music → "Tags to write" feature: a field
// disabled via UpdateSettings (the same call PUT /api/v1/settings/tagwrite
// makes — see internal/api/settings.go's tagWriteToggles) must not land on
// a real written file, even though the underlying data (Mood, here) is
// genuinely available and would otherwise be written — internal/tagwriter's
// own tests already prove Toggles works in isolation; this proves the
// live setting actually reaches WriteTags through Scanner.getTagToggles.
func TestWriteTagsRespectsDisabledFieldToggle(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	disabled := tagwriter.AllEnabled
	disabled.Mood = false
	s.UpdateSettings(s.getNamingFormat(), s.getMinMatchConfidence(), s.getOrganizeOnMatch(), disabled)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetAlbumDescription(album.ID, "A dark, hypnotic record.", "Trippy", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000, "", "", "")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	if vals := got[taglibpkg.Mood]; len(vals) != 0 {
		t.Errorf("Mood = %v, want empty — Mood is disabled via UpdateSettings", vals)
	}
	if vals := got[taglibpkg.Title]; len(vals) == 0 || vals[0] != "Alpha and Omega" {
		t.Errorf("Title = %v, want [Alpha and Omega] — Title was never disabled", vals)
	}
}

// TestWriteTagsEmbedsComposer confirms track.Composer (resolved from
// MusicBrainz work-relations at match time — see
// musicbrainz.Recording.Composer) reaches the file's own COMPOSER tag.
func TestWriteTagsEmbedsComposer(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Jeff Buckley", "Buckley, Jeff")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Grace", "1994", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-mbid", "Hallelujah", 1, 1, 200000, "", "", "Leonard Cohen")
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	if vals := got[taglibpkg.Composer]; len(vals) == 0 || vals[0] != "Leonard Cohen" {
		t.Errorf("Composer = %v, want [Leonard Cohen]", vals)
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

	if err := s.WriteTags(context.Background(), tf.ID, false); err == nil {
		t.Error("expected an error writing tags for an unmatched file")
	}
}

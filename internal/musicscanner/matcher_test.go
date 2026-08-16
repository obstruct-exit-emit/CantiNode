package musicscanner

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// buildFLACFile writes a minimal FLAC file (just a Vorbis comment block —
// dhowden/tag doesn't require STREAMINFO to parse tags) with the given
// comments, so Scanner has something real to Read()/match against.
func buildFLACFile(t *testing.T, dir, name string, comments map[string]string) string {
	t.Helper()

	var block bytes.Buffer
	writeU32LE(&block, 0) // vendor length
	writeU32LE(&block, uint32(len(comments)))
	for k, v := range comments {
		c := k + "=" + v
		writeU32LE(&block, uint32(len(c)))
		block.WriteString(c)
	}

	var file bytes.Buffer
	file.WriteString("fLaC")
	file.WriteByte(0x80 | 4)
	blockBytes := block.Bytes()
	n := len(blockBytes)
	file.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)})
	file.Write(blockBytes)

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeU32LE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

// mbRecording is a JSON-shaped stand-in for a MusicBrainz recording
// response, used by testMusicBrainzServer.
type mbRecording struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Length       int    `json:"length"`
	Score        int    `json:"score,omitempty"`
	ArtistCredit []struct {
		Name   string `json:"name"`
		Artist struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			SortName string `json:"sort-name"`
		} `json:"artist"`
	} `json:"artist-credit"`
	Releases []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ReleaseGroup struct {
			ID             string   `json:"id"`
			Title          string   `json:"title"`
			PrimaryType    string   `json:"primary-type"`
			SecondaryTypes []string `json:"secondary-types,omitempty"`
		} `json:"release-group"`
	} `json:"releases"`
}

func sampleRecording(id string, score int) mbRecording {
	rec := mbRecording{ID: id, Title: "Alpha and Omega", Length: 202000, Score: score}
	rec.ArtistCredit = []struct {
		Name   string `json:"name"`
		Artist struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			SortName string `json:"sort-name"`
		} `json:"artist"`
	}{{Name: "Boards of Canada", Artist: struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	}{ID: "artist-mbid", Name: "Boards of Canada", SortName: "Boards of Canada"}}}
	rec.Releases = []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ReleaseGroup struct {
			ID             string   `json:"id"`
			Title          string   `json:"title"`
			PrimaryType    string   `json:"primary-type"`
			SecondaryTypes []string `json:"secondary-types,omitempty"`
		} `json:"release-group"`
	}{{ID: "release-mbid", Title: "Geogaddi", Date: "2002-02-04", ReleaseGroup: struct {
		ID             string   `json:"id"`
		Title          string   `json:"title"`
		PrimaryType    string   `json:"primary-type"`
		SecondaryTypes []string `json:"secondary-types,omitempty"`
	}{ID: "rg-mbid", Title: "Geogaddi", PrimaryType: "Album"}}}
	return rec
}

// sampleCompilationTrackRecording is sampleRecording's Various-Artists
// counterpart: id's own recording is credited to performerName (the real
// per-track performer), but its best release is flagged as a compilation
// (SecondaryTypes: ["Compilation"]) — the shape correctArtistCreditForCompilation
// looks for to trigger its release-level artist-credit correction.
func sampleCompilationTrackRecording(id, performerName string) mbRecording {
	rec := mbRecording{ID: id, Title: "In the Air Tonight", Length: 202000}
	rec.ArtistCredit = []struct {
		Name   string `json:"name"`
		Artist struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			SortName string `json:"sort-name"`
		} `json:"artist"`
	}{{Name: performerName, Artist: struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	}{ID: "performer-mbid-" + performerName, Name: performerName, SortName: performerName}}}
	rec.Releases = []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ReleaseGroup struct {
			ID             string   `json:"id"`
			Title          string   `json:"title"`
			PrimaryType    string   `json:"primary-type"`
			SecondaryTypes []string `json:"secondary-types,omitempty"`
		} `json:"release-group"`
	}{{ID: "va-release-mbid", Title: "Now That's What I Call Music", Date: "1998", ReleaseGroup: struct {
		ID             string   `json:"id"`
		Title          string   `json:"title"`
		PrimaryType    string   `json:"primary-type"`
		SecondaryTypes []string `json:"secondary-types,omitempty"`
	}{ID: "va-rg-mbid", Title: "Now That's What I Call Music", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}}}}
	return rec
}

// sampleReleaseArtistCredit is a lookupResponses fixture for the
// LookupReleaseWithTracklist call correctArtistCreditForCompilation makes
// — only ArtistCredit is populated since that's the only field the fix
// reads; the shared test server encodes it via the same mbRecording JSON
// shape as any other lookup, which is fine here since ReleaseWithTracklist
// decodes only the fields it recognizes (see newTestScanner's own comment).
func sampleReleaseArtistCredit(releaseMBID, artistName string) mbRecording {
	rec := mbRecording{ID: releaseMBID, Title: "Now That's What I Call Music"}
	rec.ArtistCredit = []struct {
		Name   string `json:"name"`
		Artist struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			SortName string `json:"sort-name"`
		} `json:"artist"`
	}{{Name: artistName, Artist: struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	}{ID: "va-artist-mbid", Name: artistName, SortName: artistName}}}
	return rec
}

// newTestScanner wires up a Scanner against an in-memory database and a
// MusicBrainz client pointed at a local httptest server that serves
// lookupResponses (keyed by recording MBID, for LookupRecording) and
// searchResponse (for every SearchRecordings call, regardless of query).
func newTestScanner(t *testing.T, lookupResponses map[string]mbRecording, searchResponse []mbRecording) (*Scanner, musiclibrary.RootFolder) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/recording/" {
			json.NewEncoder(w).Encode(map[string]any{"count": len(searchResponse), "recordings": searchResponse})
			return
		}
		mbid := filepath.Base(r.URL.Path)
		rec, ok := lookupResponses[mbid]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(rec)
	}))
	t.Cleanup(srv.Close)

	sqlDB, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db := musiclibrary.NewStore(sqlDB)

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", srv.URL)

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

	s := New(db, mb, slog.Default(), "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false)
	return s, *rf
}

func TestScanRootFolderMatchesDirectMBID(t *testing.T) {
	lookupResponses := map[string]mbRecording{
		"rec-mbid": sampleRecording("rec-mbid", 0),
	}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":               "Alpha and Omega",
		"ARTIST":              "Boards of Canada",
		"MUSICBRAINZ_TRACKID": "rec-mbid",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.FilesFound != 1 || result.FilesMatched != 1 {
		t.Errorf("result = %+v, want 1 found and 1 matched", result)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}
	if matched[0].MatchConfidence != 1.0 {
		t.Errorf("MatchConfidence = %v, want 1.0 for a direct MBID match", matched[0].MatchConfidence)
	}

	track, err := s.db.GetTrack(*matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if track.Title != "Alpha and Omega" {
		t.Errorf("Track.Title = %q", track.Title)
	}
}

// TestMatchFileDirectFilesCompilationTrackUnderReleaseArtist is the
// regression test for a real bug found live: a Various Artists
// compilation ripped with each file's own correct MusicBrainz recording ID
// already embedded (the well-tagged, common case — matchFileDirect's own
// fast path) filed every track under its own real per-track performer
// instead of the one shared compilation artist/album, because applyMatch's
// artist assignment used the recording's own ArtistCredit rather than the
// release's. correctArtistCreditForCompilation must substitute the
// release's own credit for filing while still preserving the track's real
// performer as its own display credit.
func TestMatchFileDirectFilesCompilationTrackUnderReleaseArtist(t *testing.T) {
	lookupResponses := map[string]mbRecording{
		"rec-va-1":        sampleCompilationTrackRecording("rec-va-1", "Phil Collins"),
		"va-release-mbid": sampleReleaseArtistCredit("va-release-mbid", "Various Artists"),
	}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":               "In the Air Tonight",
		"ARTIST":              "Phil Collins",
		"MUSICBRAINZ_TRACKID": "rec-va-1",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("result = %+v, want 1 matched", result)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}

	track, err := s.db.GetTrack(*matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetAlbum(track.AlbumID)
	if err != nil {
		t.Fatal(err)
	}
	artist, err := s.db.GetArtist(album.ArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Various Artists" {
		t.Errorf("filed under artist %q, want the release's own Various Artists credit, not the track's real performer", artist.Name)
	}
	if track.ArtistCredit != "Phil Collins" {
		t.Errorf("Track.ArtistCredit = %q, want the real performer Phil Collins preserved for display", track.ArtistCredit)
	}
}

// TestMatchFilesUnderTrackedSeriesArtist is the end-to-end regression test
// for the MusicBrainz-series-add feature's core fix: a release group
// tracked as part of a monitored series must file under that series
// artist, not the recording/release's own real MusicBrainz artist-credit
// ("Various Artists" here — a completely different artists row). Without
// applyMatch's series check, a user who "+Add"s a series entry from its
// Missing section would grab/match it straight into a stray "Various
// Artists" artist instead, leaving the wanted row under the series artist
// stuck forever.
func TestMatchFilesUnderTrackedSeriesArtist(t *testing.T) {
	lookupResponses := map[string]mbRecording{
		"rec-va-1":        sampleCompilationTrackRecording("rec-va-1", "Phil Collins"),
		"va-release-mbid": sampleReleaseArtistCredit("va-release-mbid", "Various Artists"),
	}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	seriesArtist, err := s.db.GetOrCreateSeriesArtist("series-mbid", "Now That's What I Call Music!")
	if err != nil {
		t.Fatal(err)
	}
	// va-rg-mbid is sampleCompilationTrackRecording's release group — the
	// mismatch this test exercises: the recording's own real performer
	// (Phil Collins) and the release's own credit (Various Artists) are
	// both different from the series artist this release group is
	// actually tracked under.
	if err := s.db.ReplaceArtistReleaseGroups(seriesArtist.ID, []musiclibrary.ReleaseGroupCache{
		{ReleaseGroupMBID: "va-rg-mbid", Title: "Now That's What I Call Music", PrimaryType: "Album"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.GetOrCreateWantedAlbum(seriesArtist.ID, "va-rg-mbid", "Now That's What I Call Music", "Album", "1998"); err != nil {
		t.Fatal(err)
	}

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":               "In the Air Tonight",
		"ARTIST":              "Phil Collins",
		"MUSICBRAINZ_TRACKID": "rec-va-1",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("result = %+v, want 1 matched", result)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}

	track, err := s.db.GetTrack(*matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetAlbum(track.AlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if album.ArtistID != seriesArtist.ID {
		artist, _ := s.db.GetArtist(album.ArtistID)
		t.Errorf("album filed under artist %+v, want the series artist %d (Now That's What I Call Music!)", artist, seriesArtist.ID)
	}
	if track.ArtistCredit != "Phil Collins" {
		t.Errorf("Track.ArtistCredit = %q, want the real performer Phil Collins preserved for display", track.ArtistCredit)
	}

	wanted, err := s.db.ListWantedAlbumsByArtist(seriesArtist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 0 {
		t.Errorf("wanted albums under the series artist = %+v, want none — matching should have cleared it", wanted)
	}
}

// TestMatchClearsStaleWantedAlbum is the regression test for a real bug
// found live: an album added to Wanted before its files happened to
// already be sitting unmatched on disk (or matched through any path other
// than the grab→import pipeline, which is the only place that already
// clears a wanted_albums row) kept showing up as a second, wanted-looking
// copy of the very same release group right alongside the newly-owned
// one — different cover art too, since the wanted copy's art resolves via
// the release group's own representative release, not necessarily the
// specific release the file matched.
func TestMatchClearsStaleWantedAlbum(t *testing.T) {
	lookupResponses := map[string]mbRecording{
		"rec-mbid": sampleRecording("rec-mbid", 0), // artist-mbid / rg-mbid
	}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	artist, err := s.db.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04"); err != nil {
		t.Fatal(err)
	}

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":               "Alpha and Omega",
		"ARTIST":              "Boards of Canada",
		"MUSICBRAINZ_TRACKID": "rec-mbid",
	})

	if _, err := s.ScanRootFolder(ctx, rf); err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}

	wanted, err := s.db.ListWantedAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 0 {
		t.Errorf("wanted albums after match = %+v, want empty (the album is now owned, not still wanted)", wanted)
	}
}

func TestScanRootFolderFuzzyMatchAboveThreshold(t *testing.T) {
	s, rf := newTestScanner(t, nil, []mbRecording{sampleRecording("rec-mbid", 90)})
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":  "Alpha and Omega",
		"ARTIST": "Boards of Canada",
		"ALBUM":  "Geogaddi",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Errorf("FilesMatched = %d, want 1 (score 90 >= default threshold 0.75)", result.FilesMatched)
	}
}

// TestScanRootFolderFuzzyMatchFilesCompilationTrackUnderReleaseArtist is
// TestMatchFileDirectFilesCompilationTrackUnderReleaseArtist's companion
// for matchFileFuzzy — the standalone-file fallback path has the exact
// same bug independently (correctArtistCreditForCompilation must run
// there too, not just on the embedded-MBID fast path).
func TestScanRootFolderFuzzyMatchFilesCompilationTrackUnderReleaseArtist(t *testing.T) {
	found := sampleCompilationTrackRecording("rec-va-2", "Duran Duran")
	found.Score = 90
	lookupResponses := map[string]mbRecording{
		"va-release-mbid": sampleReleaseArtistCredit("va-release-mbid", "Various Artists"),
	}
	s, rf := newTestScanner(t, lookupResponses, []mbRecording{found})
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":  "Rio",
		"ARTIST": "Duran Duran",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("result = %+v, want 1 matched", result)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}
	track, err := s.db.GetTrack(*matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetAlbum(track.AlbumID)
	if err != nil {
		t.Fatal(err)
	}
	artist, err := s.db.GetArtist(album.ArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if artist.Name != "Various Artists" {
		t.Errorf("filed under artist %q, want the release's own Various Artists credit, not the track's real performer", artist.Name)
	}
	if track.ArtistCredit != "Duran Duran" {
		t.Errorf("Track.ArtistCredit = %q, want the real performer Duran Duran preserved for display", track.ArtistCredit)
	}
}

func TestScanRootFolderFuzzyMatchBelowThresholdStaysUnmatched(t *testing.T) {
	s, rf := newTestScanner(t, nil, []mbRecording{sampleRecording("rec-mbid", 40)})
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":  "Some Song",
		"ARTIST": "Some Artist",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 0 {
		t.Errorf("FilesMatched = %d, want 0 (score 40 below default threshold 75)", result.FilesMatched)
	}

	unmatched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusUnmatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmatched) != 1 {
		t.Fatalf("len(unmatched) = %d, want 1", len(unmatched))
	}
}

func TestScanRootFolderRescanDoesNotClearExistingMatch(t *testing.T) {
	lookupResponses := map[string]mbRecording{"rec-mbid": sampleRecording("rec-mbid", 0)}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"TITLE":               "Alpha and Omega",
		"ARTIST":              "Boards of Canada",
		"MUSICBRAINZ_TRACKID": "rec-mbid",
	})

	if _, err := s.ScanRootFolder(ctx, rf); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	// Second scan must not re-call MusicBrainz for an already-matched file
	// — if it tried, the lookup server would still succeed here, so this
	// mainly guards against a future regression that starts re-deciding
	// settled matches. What we actually assert: the match survives.
	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if result.FilesMatched != 0 {
		t.Errorf("second scan FilesMatched = %d, want 0 (nothing new to match)", result.FilesMatched)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Errorf("len(matched) after rescan = %d, want 1 (match should persist)", len(matched))
	}
}

// TestScanRootFolderDedupesAlbumAcrossDifferentReleaseEditions is the
// regression test for the "one album showed up as several library cards"
// bug: two files whose own embedded recording IDs each independently
// resolve (via musicbrainz.Recording.BestRelease) to a *different*
// release edition of the very same release group must still collapse
// into a single albums row, since that's what actually happened with a
// real Derek and the Dominos grab — different tracks' recordings each
// pointed at a different "Layla and Other Assorted Love Songs" pressing.
func TestScanRootFolderDedupesAlbumAcrossDifferentReleaseEditions(t *testing.T) {
	recA := sampleRecording("rec-a", 0)
	recA.Releases[0].ID = "release-edition-a"
	recA.Releases[0].Date = "2011"
	recB := sampleRecording("rec-b", 0)
	recB.Releases[0].ID = "release-edition-b"
	recB.Releases[0].Date = "1989"
	// Both recordings' releases share sampleRecording's own "rg-mbid"
	// release group — same canonical album, two different pressings.

	lookupResponses := map[string]mbRecording{"rec-a": recA, "rec-b": recB}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "one.flac", map[string]string{
		"TITLE": "Alpha and Omega", "ARTIST": "Boards of Canada", "MUSICBRAINZ_TRACKID": "rec-a",
	})
	buildFLACFile(t, rf.Path, "two.flac", map[string]string{
		"TITLE": "Beta", "ARTIST": "Boards of Canada", "MUSICBRAINZ_TRACKID": "rec-b",
	})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2", result.FilesMatched)
	}

	artist, err := s.db.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	albums, err := s.db.ListAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatalf("ListAlbumsByArtist: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("len(albums) = %d, want 1 (two release editions of the same release group must collapse into one album)", len(albums))
	}
}

func TestManualMatchAndClearMatch(t *testing.T) {
	lookupResponses := map[string]mbRecording{"rec-mbid": sampleRecording("rec-mbid", 0)}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ManualMatch(ctx, tf.ID, "rec-mbid", ""); err != nil {
		t.Fatalf("ManualMatch: %v", err)
	}
	got, err := s.db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchStatus != musiclibrary.StatusManual || got.TrackID == nil {
		t.Errorf("after ManualMatch: status=%q trackID=%v", got.MatchStatus, got.TrackID)
	}

	if err := s.ClearMatch(tf.ID); err != nil {
		t.Fatalf("ClearMatch: %v", err)
	}
	got, err = s.db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchStatus != musiclibrary.StatusUnmatched || got.TrackID != nil {
		t.Errorf("after ClearMatch: status=%q trackID=%v, want unmatched/nil", got.MatchStatus, got.TrackID)
	}
}

func TestDeleteTrackFileRemovesFileAndRow(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTrackFile(tf.ID); err != nil {
		t.Fatalf("DeleteTrackFile: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone from disk, stat err = %v", err)
	}
	if _, err := s.db.GetTrackFile(tf.ID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetTrackFile after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteTrackFileToleratesAlreadyMissingFile(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTrackFile(tf.ID); err != nil {
		t.Fatalf("DeleteTrackFile should tolerate an already-missing file: %v", err)
	}
	if _, err := s.db.GetTrackFile(tf.ID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetTrackFile after delete: err = %v, want ErrNotFound", err)
	}
}

// TestClearMatchReapsAlbumLeftWithZeroFiles is the regression test for a
// real dead end an album could otherwise land in: ListAlbumsByArtist
// requires a linked file to count as owned, and
// ListMissingArtistReleaseGroups excludes any release group that already
// has an albums row — so an album whose only file gets its match cleared
// used to keep its now-empty albums row forever, invisible in Owned,
// Missing, and Wanted all at once. ClearMatch must now reap it back out.
func TestClearMatchReapsAlbumLeftWithZeroFiles(t *testing.T) {
	lookupResponses := map[string]mbRecording{"rec-mbid": sampleRecording("rec-mbid", 0)}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ManualMatch(ctx, tf.ID, "rec-mbid", ""); err != nil {
		t.Fatalf("ManualMatch: %v", err)
	}
	matched, err := s.db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetTrack(*matched.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	albumID := track.AlbumID
	if _, err := s.db.GetAlbum(albumID); err != nil {
		t.Fatalf("album should exist right after matching: %v", err)
	}

	if err := s.ClearMatch(tf.ID); err != nil {
		t.Fatalf("ClearMatch: %v", err)
	}

	if _, err := s.db.GetAlbum(albumID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetAlbum after clearing its only file: err = %v, want ErrNotFound (reaped)", err)
	}
}

// TestClearMatchKeepsAlbumWithRemainingFiles proves the reap in
// TestClearMatchReapsAlbumLeftWithZeroFiles is scoped correctly — an
// album with other still-matched files must survive.
func TestClearMatchKeepsAlbumWithRemainingFiles(t *testing.T) {
	lookupResponses := map[string]mbRecording{
		"rec-1": sampleRecording("rec-1", 0),
		"rec-2": sampleRecording("rec-2", 0),
	}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	path1 := buildFLACFile(t, rf.Path, "song1.flac", map[string]string{"TITLE": "Untitled"})
	tf1, err := s.db.UpsertTrackFileByPath(rf.ID, path1, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	path2 := buildFLACFile(t, rf.Path, "song2.flac", map[string]string{"TITLE": "Untitled"})
	tf2, err := s.db.UpsertTrackFileByPath(rf.ID, path2, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ManualMatch(ctx, tf1.ID, "rec-1", ""); err != nil {
		t.Fatalf("ManualMatch tf1: %v", err)
	}
	if err := s.ManualMatch(ctx, tf2.ID, "rec-2", ""); err != nil {
		t.Fatalf("ManualMatch tf2: %v", err)
	}
	matched1, err := s.db.GetTrackFile(tf1.ID)
	if err != nil {
		t.Fatal(err)
	}
	track1, err := s.db.GetTrack(*matched1.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	albumID := track1.AlbumID

	if err := s.ClearMatch(tf1.ID); err != nil {
		t.Fatalf("ClearMatch tf1: %v", err)
	}
	if _, err := s.db.GetAlbum(albumID); err != nil {
		t.Errorf("album should still exist — tf2 still owns a file under it: %v", err)
	}
}

// TestDeleteTrackFileReapsAlbumLeftWithZeroFiles mirrors
// TestClearMatchReapsAlbumLeftWithZeroFiles for the other path that can
// strip an album down to zero files: outright deleting its last file.
func TestDeleteTrackFileReapsAlbumLeftWithZeroFiles(t *testing.T) {
	lookupResponses := map[string]mbRecording{"rec-mbid": sampleRecording("rec-mbid", 0)}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ManualMatch(ctx, tf.ID, "rec-mbid", ""); err != nil {
		t.Fatalf("ManualMatch: %v", err)
	}
	matched, err := s.db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetTrack(*matched.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	albumID := track.AlbumID

	if err := s.DeleteTrackFile(tf.ID); err != nil {
		t.Fatalf("DeleteTrackFile: %v", err)
	}

	if _, err := s.db.GetAlbum(albumID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetAlbum after deleting its only file: err = %v, want ErrNotFound (reaped)", err)
	}
}

// TestScanResultErrorsEmptyIsNotNil guards against the same nil-slice-
// marshals-to-null bug found in internal/musiclibrary's List* methods —
// ScanResult.Errors goes straight into the GET /api/v1/scan/status JSON
// response.
func TestScanResultErrorsEmptyIsNotNil(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)
	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.Errors == nil {
		t.Error("ScanResult.Errors is nil for a clean scan, want a non-nil empty slice")
	}
}

func TestScanRootFolderRemovesDeletedFiles(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	if _, err := s.ScanRootFolder(ctx, rf); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if result.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want 1", result.FilesRemoved)
	}

	remaining, err := s.db.ListTrackFilesByRootFolder(rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("len(remaining) = %d, want 0", len(remaining))
	}
}

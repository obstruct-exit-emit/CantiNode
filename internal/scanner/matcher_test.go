package scanner

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
			ID          string `json:"id"`
			Title       string `json:"title"`
			PrimaryType string `json:"primary-type"`
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
			ID          string `json:"id"`
			Title       string `json:"title"`
			PrimaryType string `json:"primary-type"`
		} `json:"release-group"`
	}{{ID: "release-mbid", Title: "Geogaddi", Date: "2002-02-04", ReleaseGroup: struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		PrimaryType string `json:"primary-type"`
	}{ID: "rg-mbid", Title: "Geogaddi", PrimaryType: "Album"}}}
	return rec
}

// newTestScanner wires up a Scanner against an in-memory database and a
// MusicBrainz client pointed at a local httptest server that serves
// lookupResponses (keyed by recording MBID, for LookupRecording) and
// searchResponse (for every SearchRecordings call, regardless of query).
func newTestScanner(t *testing.T, lookupResponses map[string]mbRecording, searchResponse []mbRecording) (*Scanner, database.RootFolder) {
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

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", srv.URL)

	rootDir := t.TempDir()
	rf, err := db.CreateRootFolder(t.Context(), rootDir)
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

	matched, err := s.db.ListTrackFilesByStatus(ctx, database.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}
	if matched[0].MatchConfidence != 1.0 {
		t.Errorf("MatchConfidence = %v, want 1.0 for a direct MBID match", matched[0].MatchConfidence)
	}

	track, err := s.db.GetTrack(ctx, *matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if track.Title != "Alpha and Omega" {
		t.Errorf("Track.Title = %q", track.Title)
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

	unmatched, err := s.db.ListTrackFilesByStatus(ctx, database.StatusUnmatched)
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

	matched, err := s.db.ListTrackFilesByStatus(ctx, database.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Errorf("len(matched) after rescan = %d, want 1 (match should persist)", len(matched))
	}
}

func TestManualMatchAndClearMatch(t *testing.T) {
	lookupResponses := map[string]mbRecording{"rec-mbid": sampleRecording("rec-mbid", 0)}
	s, rf := newTestScanner(t, lookupResponses, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ManualMatch(ctx, tf.ID, "rec-mbid", ""); err != nil {
		t.Fatalf("ManualMatch: %v", err)
	}
	got, err := s.db.GetTrackFile(ctx, tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchStatus != database.StatusManual || got.TrackID == nil {
		t.Errorf("after ManualMatch: status=%q trackID=%v", got.MatchStatus, got.TrackID)
	}

	if err := s.ClearMatch(ctx, tf.ID); err != nil {
		t.Fatalf("ClearMatch: %v", err)
	}
	got, err = s.db.GetTrackFile(ctx, tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchStatus != database.StatusUnmatched || got.TrackID != nil {
		t.Errorf("after ClearMatch: status=%q trackID=%v, want unmatched/nil", got.MatchStatus, got.TrackID)
	}
}

func TestDeleteTrackFileRemovesFileAndRow(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTrackFile(ctx, tf.ID); err != nil {
		t.Fatalf("DeleteTrackFile: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone from disk, stat err = %v", err)
	}
	if _, err := s.db.GetTrackFile(ctx, tf.ID); err != database.ErrNotFound {
		t.Errorf("GetTrackFile after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteTrackFileToleratesAlreadyMissingFile(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)
	ctx := t.Context()

	path := buildFLACFile(t, rf.Path, "song.flac", map[string]string{"TITLE": "Untitled"})
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, path, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTrackFile(ctx, tf.ID); err != nil {
		t.Fatalf("DeleteTrackFile should tolerate an already-missing file: %v", err)
	}
	if _, err := s.db.GetTrackFile(ctx, tf.ID); err != database.ErrNotFound {
		t.Errorf("GetTrackFile after delete: err = %v, want ErrNotFound", err)
	}
}

// TestScanResultErrorsEmptyIsNotNil guards against the same nil-slice-
// marshals-to-null bug found in internal/database's List* methods (see
// database.TestListRootFoldersEmptyIsNotNil) — ScanResult.Errors goes
// straight into the GET /api/v1/scan/status JSON response.
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

	remaining, err := s.db.ListTrackFilesByRootFolder(ctx, rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("len(remaining) = %d, want 0", len(remaining))
	}
}

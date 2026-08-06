package scanner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
)

// --- JSON-shaped fixtures for release-search/release-lookup responses,
// mirroring the real MusicBrainz wire format the same way matcher_test.go's
// mbRecording mirrors the recording one (kept independent of
// internal/musicbrainz's own types so these tests exercise the actual
// wire contract, not just Go-to-Go plumbing). ---

type mbArtistRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	SortName string `json:"sort-name"`
}

type mbArtistCredit struct {
	Name   string      `json:"name"`
	Artist mbArtistRef `json:"artist"`
}

type mbReleaseGroup struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	PrimaryType string `json:"primary-type"`
}

type mbReleaseSearchResult struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Date         string           `json:"date"`
	Score        int              `json:"score"`
	TrackCount   int              `json:"track-count"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
	ReleaseGroup mbReleaseGroup   `json:"release-group"`
}

type mbTrackRecording struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Length int    `json:"length"`
}

type mbReleaseTrack struct {
	Position  int              `json:"position"`
	Number    string           `json:"number"`
	Title     string           `json:"title"`
	Length    int              `json:"length"`
	Recording mbTrackRecording `json:"recording"`
}

type mbMedium struct {
	Format     string           `json:"format"`
	Position   int              `json:"position"`
	TrackCount int              `json:"track-count"`
	Tracks     []mbReleaseTrack `json:"tracks"`
}

type mbReleaseWithTracklist struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Date         string           `json:"date"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
	ReleaseGroup mbReleaseGroup   `json:"release-group"`
	Media        []mbMedium       `json:"media"`
}

// newTestAlbumRelease builds a single-medium release fixture from a
// simple list of track titles, numbered from 1 — enough for every test
// below, which only cares about title/position, not multi-disc layouts.
func newTestAlbumRelease(id, title string, trackTitles ...string) mbReleaseWithTracklist {
	tracks := make([]mbReleaseTrack, len(trackTitles))
	for i, title := range trackTitles {
		tracks[i] = mbReleaseTrack{
			Position:  i + 1,
			Number:    "",
			Title:     title,
			Length:    200000,
			Recording: mbTrackRecording{ID: "rec-" + title, Title: title, Length: 200000},
		}
	}
	return mbReleaseWithTracklist{
		ID:    id,
		Title: title,
		ArtistCredit: []mbArtistCredit{
			{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist", SortName: "Test Artist"}},
		},
		ReleaseGroup: mbReleaseGroup{ID: "rg-" + id, Title: title, PrimaryType: "Album"},
		Media:        []mbMedium{{Format: "CD", Position: 1, TrackCount: len(tracks), Tracks: tracks}},
	}
}

// folderTestServer fakes every MusicBrainz endpoint folder-level matching
// touches: recording search (the per-file fallback), recording lookup
// (matchFileDirect), release search, and release lookup (keyed by MBID).
// Also counts requests by kind, for tests asserting call volume.
type folderTestServer struct {
	recordingSearch  []mbRecording
	recordingLookups map[string]mbRecording
	releaseSearch    []mbReleaseSearchResult
	releaseLookups   map[string]mbReleaseWithTracklist

	mu     sync.Mutex
	counts map[string]int
}

func newFolderTestServer() *folderTestServer {
	return &folderTestServer{
		recordingLookups: map[string]mbRecording{},
		releaseLookups:   map[string]mbReleaseWithTracklist{},
		counts:           map[string]int{},
	}
}

func (f *folderTestServer) count(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[kind]++
}

func (f *folderTestServer) countOf(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[kind]
}

func (f *folderTestServer) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *folderTestServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/recording/":
		f.count("recording-search")
		json.NewEncoder(w).Encode(map[string]any{"count": len(f.recordingSearch), "recordings": f.recordingSearch})
	case strings.HasPrefix(r.URL.Path, "/recording/"):
		f.count("recording-lookup")
		mbid := filepath.Base(r.URL.Path)
		rec, ok := f.recordingLookups[mbid]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(rec)
	case r.URL.Path == "/release/":
		f.count("release-search")
		json.NewEncoder(w).Encode(map[string]any{"count": len(f.releaseSearch), "releases": f.releaseSearch})
	case strings.HasPrefix(r.URL.Path, "/release/"):
		f.count("release-lookup")
		mbid := filepath.Base(r.URL.Path)
		rel, ok := f.releaseLookups[mbid]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(rel)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// newFolderTestScanner wires a Scanner against fs (an httptest server
// serving every MusicBrainz endpoint folder matching needs) and a fresh
// in-memory database/root folder — the folder-matching sibling of
// matcher_test.go's newTestScanner.
func newFolderTestScanner(t *testing.T, fs *folderTestServer) (*Scanner, database.RootFolder) {
	t.Helper()
	url := fs.start(t)

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", url)

	rootDir := t.TempDir()
	rf, err := db.CreateRootFolder(t.Context(), rootDir)
	if err != nil {
		t.Fatal(err)
	}

	s := New(db, mb, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false)
	return s, *rf
}

func TestScanRootFolderGroupsFolderIntoOneRelease(t *testing.T) {
	fs := newFolderTestServer()
	// Each track's independent fuzzy search would point to a DIFFERENT
	// recording/release — reproducing the actual reported bug — so this
	// test only passes if folder-grouping (not per-file fallback) is what
	// actually resolved these files.
	fs.recordingSearch = []mbRecording{sampleRecording("wrong-rec", 100)}
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-main", Title: "Test Album", Score: 100, TrackCount: 3,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-main", Title: "Test Album", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-main"] = newTestAlbumRelease("rel-main", "Test Album", "Track One", "Track Two", "Track Three")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track One", "TRACKNUMBER": "1"})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track Two", "TRACKNUMBER": "2"})
	buildFLACFile(t, rf.Path, "03.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track Three", "TRACKNUMBER": "3"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 3 {
		t.Fatalf("FilesMatched = %d, want 3 (result=%+v)", result.FilesMatched, result)
	}

	albums, err := s.db.ListAlbumsByArtist(t.Context(), mustArtistID(t, s, "artist-mbid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("len(albums) = %d, want exactly 1 (this is the regression this rework fixes)", len(albums))
	}
	if albums[0].MBID != "rel-main" {
		t.Errorf("album MBID = %q, want rel-main", albums[0].MBID)
	}
}

func TestScanRootFolderDirectRecordingIDBypassesFolderGrouping(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingLookups["direct-rec"] = sampleRecording("direct-rec", 0)
	// No release search/lookup configured at all — if the direct file
	// were somehow routed through folder resolution, LookupReleaseWithTracklist
	// or SearchReleases would 404/empty, proving isolation either way.

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Direct Track",
		"MUSICBRAINZ_TRACKID": "direct-rec",
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1", result.FilesMatched)
	}
	if fs.countOf("release-search") != 0 || fs.countOf("release-lookup") != 0 {
		t.Errorf("release search/lookup should never be hit for a direct-MBID file: search=%d lookup=%d",
			fs.countOf("release-search"), fs.countOf("release-lookup"))
	}

	files, err := s.db.ListTrackFilesByRootFolder(t.Context(), rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].MatchConfidence != 1.0 {
		t.Errorf("files = %+v, want confidence 1.0", files)
	}
}

func TestScanRootFolderEmbeddedReleaseMBIDSkipsSearch(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseLookups["rel-embedded"] = newTestAlbumRelease("rel-embedded", "Embedded Album", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Embedded Album", "TITLE": "Track One", "TRACKNUMBER": "1",
		"MUSICBRAINZ_ALBUMID": "rel-embedded",
	})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Embedded Album", "TITLE": "Track Two", "TRACKNUMBER": "2",
		"MUSICBRAINZ_ALBUMID": "rel-embedded",
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 (result=%+v)", result.FilesMatched, result)
	}
	if fs.countOf("release-search") != 0 {
		t.Errorf("release-search should be skipped entirely when every file agrees on an embedded release MBID, got %d calls", fs.countOf("release-search"))
	}
	if fs.countOf("release-lookup") != 1 {
		t.Errorf("release-lookup count = %d, want exactly 1 (one lookup, reused for every file in the folder)", fs.countOf("release-lookup"))
	}
}

func TestScanRootFolderConflictingEmbeddedReleaseMBIDsFallThroughToSearch(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-consensus", Title: "Consensus Album", Score: 100, TrackCount: 2,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-consensus", Title: "Consensus Album", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-consensus"] = newTestAlbumRelease("rel-consensus", "Consensus Album", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Consensus Album", "TITLE": "Track One", "TRACKNUMBER": "1",
		"MUSICBRAINZ_ALBUMID": "rel-a",
	})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Consensus Album", "TITLE": "Track Two", "TRACKNUMBER": "2",
		"MUSICBRAINZ_ALBUMID": "rel-b", // conflicts with rel-a above
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 (should fall through to the tag-consensus search)", result.FilesMatched)
	}
	if fs.countOf("release-search") != 1 {
		t.Errorf("release-search count = %d, want 1 (conflicting embedded MBIDs should fall through to search)", fs.countOf("release-search"))
	}
}

func TestScanRootFolderInconsistentTagsFallsBackToPerFileMatching(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingSearch = []mbRecording{sampleRecording("fallback-rec", 100)}

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Artist A", "ALBUM": "Album A", "TITLE": "Song A"})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Artist B", "ALBUM": "Album B", "TITLE": "Song B"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if fs.countOf("release-search") != 0 {
		t.Errorf("release-search should never be called for a folder with inconsistent Album tags, got %d calls", fs.countOf("release-search"))
	}
	if result.FilesMatched != 2 {
		t.Errorf("FilesMatched = %d, want 2 (both should still match via independent per-file fallback)", result.FilesMatched)
	}
}

func TestScanRootFolderEmptyReleaseSearchFallsBackGracefully(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{} // no candidates found
	fs.recordingSearch = []mbRecording{sampleRecording("fallback-rec", 100)}

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track One"})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track Two"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none (an empty release search is not a scan error)", result.Errors)
	}
	if result.FilesMatched != 2 {
		t.Errorf("FilesMatched = %d, want 2 (per-file fallback should still run)", result.FilesMatched)
	}
}

func TestScanRootFolderReleaseLookup404FallsBackGracefully(t *testing.T) {
	fs := newFolderTestServer()
	// releaseLookups deliberately left empty — the embedded MBID below
	// won't resolve to anything, simulating a stale/typo'd tag.
	fs.recordingSearch = []mbRecording{sampleRecording("fallback-rec", 100)}

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track One",
		"MUSICBRAINZ_ALBUMID": "does-not-exist",
	})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{
		"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track Two",
		"MUSICBRAINZ_ALBUMID": "does-not-exist",
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none (a 404 release lookup is not a scan error)", result.Errors)
	}
	if result.FilesMatched != 2 {
		t.Errorf("FilesMatched = %d, want 2 (per-file fallback should still run)", result.FilesMatched)
	}
}

func TestScanRootFolderReleaseSearchCallCountDropsForMultiTrackFolder(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-5", Title: "Five Track Album", Score: 100, TrackCount: 5,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-5", Title: "Five Track Album", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-5"] = newTestAlbumRelease("rel-5", "Five Track Album", "One", "Two", "Three", "Four", "Five")

	s, rf := newFolderTestScanner(t, fs)
	for i, title := range []string{"One", "Two", "Three", "Four", "Five"} {
		buildFLACFile(t, rf.Path, title+".flac", map[string]string{
			"ARTIST": "Test Artist", "ALBUM": "Five Track Album", "TITLE": title,
			"TRACKNUMBER": strconv.Itoa(i + 1),
		})
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 5 {
		t.Fatalf("FilesMatched = %d, want 5", result.FilesMatched)
	}
	if got := fs.countOf("release-search"); got != 1 {
		t.Errorf("release-search count = %d, want 1", got)
	}
	if got := fs.countOf("release-lookup"); got != 1 {
		t.Errorf("release-lookup count = %d, want 1", got)
	}
	if got := fs.countOf("recording-search"); got != 0 {
		t.Errorf("recording-search count = %d, want 0 (should never fall back for a resolved folder)", got)
	}
}

func TestScanRootFolderUnslottableFileStaysUnmatchedWithoutBlockingSiblings(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-partial", Title: "Partial Album", Score: 100, TrackCount: 2,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-partial", Title: "Partial Album", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-partial"] = newTestAlbumRelease("rel-partial", "Partial Album", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Partial Album", "TITLE": "Track One", "TRACKNUMBER": "1"})
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Partial Album", "TITLE": "Track Two", "TRACKNUMBER": "2"})
	// A third file that doesn't correspond to anything on the release —
	// out-of-range track number and a wildly different title, so neither
	// slotTrack signal should confidently place it.
	buildFLACFile(t, rf.Path, "03.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Partial Album", "TITLE": "Completely Unrelated Bonus Thing", "TRACKNUMBER": "99"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 (the unslottable file must not match)", result.FilesMatched)
	}

	unmatched, err := s.db.ListTrackFilesByStatus(t.Context(), database.StatusUnmatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(unmatched) != 1 || filepath.Base(unmatched[0].Path) != "03.flac" {
		t.Errorf("unmatched = %+v, want exactly 03.flac", unmatched)
	}
}

func TestScanRootFolderUsesReleaseTrackPositionNotFileTags(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-pos", Title: "Position Album", Score: 100, TrackCount: 2,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-pos", Title: "Position Album", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-pos"] = newTestAlbumRelease("rel-pos", "Position Album", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Position Album", "TITLE": "Track One", "TRACKNUMBER": "1"})
	// This file's own TRACKNUMBER tag is wrong (99, out of range), but its
	// title exactly matches release track position 2 — slotTrack must fall
	// through to title similarity and use the release's real position.
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Position Album", "TITLE": "Track Two", "TRACKNUMBER": "99"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 (result=%+v)", result.FilesMatched, result)
	}

	files, err := s.db.ListTrackFilesByRootFolder(t.Context(), rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var trackTwoFile database.TrackFile
	for _, f := range files {
		if filepath.Base(f.Path) == "02.flac" {
			trackTwoFile = f
		}
	}
	if trackTwoFile.TrackID == nil {
		t.Fatal("02.flac was not matched")
	}
	track, err := s.db.GetTrack(t.Context(), *trackTwoFile.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if track.TrackNumber != 2 {
		t.Errorf("stored TrackNumber = %d, want 2 (from the release's own position, not the file's wrong tag of 99)", track.TrackNumber)
	}
}

func TestScanRootFolderSingleRemainingFileSkipsReleaseSearch(t *testing.T) {
	fs := newFolderTestServer()

	s, rf := newFolderTestScanner(t, fs)

	// Pre-seed one file in the folder as already matched, so the walk
	// excludes it from grouping entirely, leaving exactly one unmatched
	// sibling in an otherwise multi-file directory.
	artist, err := s.db.GetOrCreateArtist(t.Context(), "artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(t.Context(), artist.ID, "rel-existing", "rg-existing", "Existing Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(t.Context(), album.ID, "rec-existing", "Already Matched", 1, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}
	existingPath := buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Existing Album", "TITLE": "Already Matched", "TRACKNUMBER": "1"})
	existingTF, err := s.db.UpsertTrackFileByPath(t.Context(), rf.ID, existingPath, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(t.Context(), existingTF.ID, &track.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	fs.recordingSearch = []mbRecording{sampleRecording("leftover-rec", 100)}
	buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Existing Album", "TITLE": "Leftover Track"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if fs.countOf("release-search") != 0 {
		t.Errorf("release-search count = %d, want 0 (a single leftover file should skip straight to per-file fallback)", fs.countOf("release-search"))
	}
	if result.FilesMatched != 1 {
		t.Errorf("FilesMatched = %d, want 1 (the leftover file, via fallback)", result.FilesMatched)
	}
}

func mustArtistID(t *testing.T, s *Scanner, mbid string) int64 {
	t.Helper()
	artist, err := s.db.GetOrCreateArtist(t.Context(), mbid, "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	return artist.ID
}

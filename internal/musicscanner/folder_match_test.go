package musicscanner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagwriter"
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
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Length       int              `json:"length"`
	ArtistCredit []mbArtistCredit `json:"artist-credit,omitempty"`
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

	// recordingBatchFails, when set, makes the batch rid:(...) endpoint
	// fail outright (a non-retryable status) instead of serving
	// recordingLookups — for testing matchDirectEntries' own fallback to
	// today's per-file matchFileDirect loop when the batch call itself
	// fails.
	recordingBatchFails bool

	// recordingBatchOmit, when set, excludes these IDs from the batch
	// rid:(...) endpoint's results even though recordingLookups has them —
	// simulates a real MusicBrainz search-index gap confirmed live: a
	// perfectly valid, resolvable-by-single-lookup recording missing from
	// an otherwise-successful multi-ID search response.
	recordingBatchOmit map[string]bool

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
		if ids, ok := parseBatchRecordingIDs(r.URL.Query().Get("query")); ok {
			f.count("recording-batch")
			if f.recordingBatchFails {
				w.WriteHeader(http.StatusBadRequest) // non-retryable, non-transient — fails immediately
				return
			}
			var recs []mbRecording
			for _, id := range ids {
				if f.recordingBatchOmit[id] {
					continue
				}
				if rec, ok := f.recordingLookups[id]; ok {
					recs = append(recs, rec)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"count": len(recs), "recordings": recs})
			return
		}
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
// database/root folder — the folder-matching sibling of matcher_test.go's
// newTestScanner.
func newFolderTestScanner(t *testing.T, fs *folderTestServer) (*Scanner, musiclibrary.RootFolder) {
	t.Helper()
	url := fs.start(t)

	sqlDB, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db := musiclibrary.NewStore(sqlDB)

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", url)

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

	s := New(db, mb, nil, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false, tagwriter.AllEnabled, false, nil)
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

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "artist-mbid"))
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
		// TITLE must agree with sampleRecording's own hardcoded "Alpha and
		// Omega" — titleAgrees (matcher.go) declines the direct-MBID fast
		// path on a title mismatch, which would defeat this test's own
		// point (proving isolation from folder resolution) for an
		// unrelated reason.
		"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Alpha and Omega",
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

	files, err := s.db.ListTrackFilesByRootFolder(rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].MatchConfidence != 1.0 {
		t.Errorf("files = %+v, want confidence 1.0", files)
	}
}

// TestScanRootFolderBatchesDirectRecordingLookups proves a folder of
// several direct-MBID files resolves in one MusicBrainz recording request,
// not one per file — the actual point of batching (see
// matchDirectEntries' own doc comment).
func TestScanRootFolderBatchesDirectRecordingLookups(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingLookups["rec-1"] = sampleRecording("rec-1", 0)
	fs.recordingLookups["rec-2"] = sampleRecording("rec-2", 0)
	fs.recordingLookups["rec-3"] = sampleRecording("rec-3", 0)

	s, rf := newFolderTestScanner(t, fs)
	for i, id := range []string{"rec-1", "rec-2", "rec-3"} {
		buildFLACFile(t, rf.Path, fmt.Sprintf("%02d.flac", i+1), map[string]string{
			"ARTIST": "Boards of Canada", "TITLE": "Alpha and Omega",
			"MUSICBRAINZ_TRACKID": id,
		})
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 3 {
		t.Fatalf("FilesMatched = %d, want 3 (result=%+v)", result.FilesMatched, result)
	}
	if got := fs.countOf("recording-batch"); got != 1 {
		t.Errorf("recording-batch count = %d, want exactly 1 — one batched request for all 3 files, not one per file", got)
	}
	if got := fs.countOf("recording-lookup"); got != 0 {
		t.Errorf("recording-lookup (single-ID) count = %d, want 0 — the batched path shouldn't fall back to per-file lookups here", got)
	}
}

// TestScanRootFolderMemoizesReleaseCreditLookupAcrossFolder is the
// regression test for a real bug found live: watching an actual scan, a
// Various Artists compilation folder's tracks visibly left Unmatched one
// at a time, several seconds apart, instead of together — even after the
// batched recording lookup fix above. Root cause: every track resolves to
// the SAME release, but correctArtistCreditForCompilation's own
// LookupReleaseWithTracklist fetch for that release wasn't batched or
// cached at all — an N-track folder paid N identical network fetches for
// data that's the same on every one. Asserts the fix (releaseCreditCache):
// 3 tracks resolving to the same release only pay for that release's
// tracklist fetch once.
func TestScanRootFolderMemoizesReleaseCreditLookupAcrossFolder(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingLookups["rec-1"] = sampleSamplerTrackRecording("rec-1", "Artist One")
	fs.recordingLookups["rec-2"] = sampleSamplerTrackRecording("rec-2", "Artist Two")
	fs.recordingLookups["rec-3"] = sampleSamplerTrackRecording("rec-3", "Artist Three")
	// sampleSamplerTrackRecording hardcodes every recording's own best
	// release to this same id — exactly the real-world shape (a whole
	// compilation's tracks all belong to the one release).
	fs.releaseLookups["sampler-release-mbid"] = mbReleaseWithTracklist{
		ID:    "sampler-release-mbid",
		Title: "Cities 97 Sampler Volume 27",
		ArtistCredit: []mbArtistCredit{
			{Name: "Various Artists", Artist: mbArtistRef{ID: "va-mbid", Name: "Various Artists", SortName: "Various Artists"}},
		},
	}

	s, rf := newFolderTestScanner(t, fs)
	for i, id := range []string{"rec-1", "rec-2", "rec-3"} {
		buildFLACFile(t, rf.Path, fmt.Sprintf("%02d.flac", i+1), map[string]string{
			"ARTIST": "Doesn't Matter", "TITLE": "Some Song",
			"MUSICBRAINZ_TRACKID": id,
		})
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 3 {
		t.Fatalf("FilesMatched = %d, want 3 (result=%+v)", result.FilesMatched, result)
	}
	if got := fs.countOf("release-lookup"); got != 1 {
		t.Errorf("release-lookup count = %d, want exactly 1 — all 3 tracks resolve to the same release, so its tracklist should only be fetched once per folder, not once per track", got)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 3 {
		t.Fatalf("matched = %+v, want 3", matched)
	}
	for _, m := range matched {
		track, err := s.db.GetTrack(*m.TrackID)
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
			t.Errorf("track %q filed under artist %q, want Various Artists", track.Title, artist.Name)
		}
	}
}

// TestScanRootFolderHonorsEmbeddedReleaseTagPastRecordingLookupCap is the
// regression test for a real live bug: LookupRecording's inc=releases is
// capped at 25 by MusicBrainz itself (confirmed live: the dedicated
// /release?recording=<mbid> browse endpoint, which does paginate
// properly, reported 35 total releases for the real recording behind
// this test). rec.Releases below stands in for that truncated response —
// it deliberately omits the file's own tagged release ("rel-single"),
// containing only an unrelated "clean album" release that would win
// Recording.BestRelease's own heuristic fallback if the real release
// were never found. Found live: a Blind Melon "Change" single track,
// correctly tagged with the single's own release MBID, got filed under
// the unrelated "Blind Melon" self-titled album instead — the single was
// the recording's 29th of 35 known releases, past the cap, so
// BestRelease never even considered it.
func TestScanRootFolderHonorsEmbeddedReleaseTagPastRecordingLookupCap(t *testing.T) {
	fs := newFolderTestServer()
	rec := mbRecording{ID: "rec-change", Title: "Change"}
	rec.ArtistCredit = []struct {
		Name   string `json:"name"`
		Artist struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			SortName string `json:"sort-name"`
		} `json:"artist"`
	}{{Name: "Blind Melon", Artist: struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	}{ID: "artist-mbid", Name: "Blind Melon", SortName: "Blind Melon"}}}
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
	}{{ID: "rel-album", Title: "Blind Melon", ReleaseGroup: struct {
		ID             string   `json:"id"`
		Title          string   `json:"title"`
		PrimaryType    string   `json:"primary-type"`
		SecondaryTypes []string `json:"secondary-types,omitempty"`
	}{ID: "rg-album", Title: "Blind Melon", PrimaryType: "Album"}}}
	fs.recordingLookups["rec-change"] = rec

	fs.releaseLookups["rel-single"] = mbReleaseWithTracklist{
		ID:    "rel-single",
		Title: "Change",
		ArtistCredit: []mbArtistCredit{
			{Name: "Blind Melon", Artist: mbArtistRef{ID: "artist-mbid", Name: "Blind Melon", SortName: "Blind Melon"}},
		},
		ReleaseGroup: mbReleaseGroup{ID: "rg-single", Title: "Change", PrimaryType: "Single"},
	}

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Blind Melon", "TITLE": "Change",
		"MUSICBRAINZ_TRACKID":        "rec-change",
		"MUSICBRAINZ_ALBUMID":        "rel-single",
		"MUSICBRAINZ_RELEASEGROUPID": "rg-single",
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1 (result=%+v)", result.FilesMatched, result)
	}

	artist, err := s.db.GetOrCreateArtist("artist-mbid", "Blind Melon", "Blind Melon")
	if err != nil {
		t.Fatal(err)
	}
	albums, err := s.db.ListAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 {
		t.Fatalf("len(albums) = %d, want 1", len(albums))
	}
	if albums[0].ReleaseGroupMBID != "rg-single" {
		t.Errorf("album release group = %q, want %q (the file's own embedded release tag) — got the unrelated \"clean album\" release group instead, meaning BestRelease's heuristic fallback won despite the file's own correct tag", albums[0].ReleaseGroupMBID, "rg-single")
	}
}

// TestScanRootFolderNeverFilesUnderWrongArtistWhenCorrectionFetchFails is
// the regression test for a real bug found live: correctArtistCreditForCompilation's
// own LookupReleaseWithTracklist call failed (a genuine live network
// hiccup, not a data problem — the real MusicBrainz data for this exact
// release was confirmed correct via both the batch and single-lookup
// endpoints after the fact) for one Various Artists compilation track,
// and the failure silently degraded to filing the file under its own
// real per-track performer (a full-confidence WRONG match, with no error
// anywhere to notice) instead of leaving it for a real second attempt.
// Worse, once matched, that wrong album's own mbid then permanently
// "won" any later correction attempt too, via GetOrCreateAlbum's own
// ON CONFLICT(mbid) recovery — the mismatch was completely self-
// reinforcing. Confirms the fix: a correction-fetch failure (simulated
// here as the release simply never answering — releaseLookups has no
// entry for it) makes the direct fast path decline (same auto-route as
// an embedded-tag inconsistency) rather than silently locking in the
// wrong artist; with nothing else in this single-file folder able to
// resolve it either, the file ends up a genuine, visible scan error —
// never a confident wrong match.
func TestScanRootFolderNeverFilesUnderWrongArtistWhenCorrectionFetchFails(t *testing.T) {
	fs := newFolderTestServer()
	rec := sampleSamplerTrackRecording("rec-1", "Little Feat")
	fs.recordingLookups["rec-1"] = rec
	// Deliberately no fs.releaseLookups["sampler-release-mbid"] entry at
	// all — every LookupReleaseWithTracklist call for it 404s, standing in
	// for a real transient failure.
	fuzzyCandidate := rec
	fuzzyCandidate.Score = 100                         // clears matchFileFuzzy's own confidence gate, so it actually reaches the correction check
	fs.recordingSearch = []mbRecording{fuzzyCandidate} // matchFileFuzzy's own fallback candidate, once folder consensus also can't resolve it

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{
		"ARTIST": "Little Feat", "TITLE": "Some Song",
		"MUSICBRAINZ_TRACKID": "rec-1",
		"MUSICBRAINZ_ALBUMID": "sampler-release-mbid",
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 0 {
		t.Errorf("FilesMatched = %d, want 0 — must never confidently match under the wrong (per-track) artist when the compilation-credit check itself failed", result.FilesMatched)
	}
	if len(result.Errors) == 0 {
		t.Error("Errors is empty, want at least one — a genuine correction-check failure should surface visibly, not vanish silently")
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 0 {
		t.Errorf("matched = %+v, want none", matched)
	}
}

// TestScanRootFolderFallsBackToPerFileLookupWhenBatchOmitsOneRecording is
// the regression test for a real bug found live: MusicBrainz's search
// index (what the batch rid:(...) endpoint queries) can have real gaps
// relative to its own authoritative per-ID lookup endpoint — a genuine,
// fully valid, in-catalog recording (confirmed live against the real API)
// came back completely absent from an 18-ID batch search that correctly
// returned the other 17. Treating a batch miss as "doesn't exist" would
// have wrongly left a real file unmatched forever; matchDirectEntries must
// instead give it one authoritative shot via the single-lookup path
// (matchFileDirect) before giving up.
func TestScanRootFolderFallsBackToPerFileLookupWhenBatchOmitsOneRecording(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingLookups["rec-1"] = sampleRecording("rec-1", 0)
	fs.recordingLookups["rec-2"] = sampleRecording("rec-2", 0)
	fs.recordingBatchOmit = map[string]bool{"rec-2": true}

	s, rf := newFolderTestScanner(t, fs)
	for i, id := range []string{"rec-1", "rec-2"} {
		buildFLACFile(t, rf.Path, fmt.Sprintf("%02d.flac", i+1), map[string]string{
			"ARTIST": "Boards of Canada", "TITLE": "Alpha and Omega",
			"MUSICBRAINZ_TRACKID": id,
		})
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 — a recording missing from the batch search results must still get a real single-lookup shot, not be treated as gone (result=%+v)", result.FilesMatched, result)
	}
	if got := fs.countOf("recording-batch"); got != 1 {
		t.Errorf("recording-batch count = %d, want exactly 1 (the original batch attempt)", got)
	}
	if got := fs.countOf("recording-lookup"); got != 1 {
		t.Errorf("recording-lookup (single-ID) count = %d, want exactly 1 — only the one recording missing from the batch should fall back, not both", got)
	}
}

// TestScanRootFolderFallsBackToPerFileLookupWhenBatchFails proves the
// never-worse-than-before guarantee: if the batch rid:(...) request itself
// fails outright (network/bad-request/etc.), every direct-MBID file still
// gets matched via today's per-file matchFileDirect loop instead of being
// left unmatched.
func TestScanRootFolderFallsBackToPerFileLookupWhenBatchFails(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingBatchFails = true
	fs.recordingLookups["rec-1"] = sampleRecording("rec-1", 0)
	fs.recordingLookups["rec-2"] = sampleRecording("rec-2", 0)

	s, rf := newFolderTestScanner(t, fs)
	for i, id := range []string{"rec-1", "rec-2"} {
		buildFLACFile(t, rf.Path, fmt.Sprintf("%02d.flac", i+1), map[string]string{
			"ARTIST": "Boards of Canada", "TITLE": "Alpha and Omega",
			"MUSICBRAINZ_TRACKID": id,
		})
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 — a failed batch call must fall back to per-file matching, not leave files unmatched (result=%+v)", result.FilesMatched, result)
	}
	if got := fs.countOf("recording-lookup"); got != 2 {
		t.Errorf("recording-lookup count = %d, want 2 — one per-file fallback lookup for each direct-MBID file", got)
	}
}

// TestScanRootFolderAutoRoutesDisagreeingDirectMatchToFolderConsensus is
// the end-to-end regression test for the auto-route fix: a file whose
// embedded recording ID disagrees with its own release-group tag no
// longer just sits unmatched — it gets a real shot at whole-folder
// consensus matching, using the correct MusicBrainzAlbumID tag the bad
// recording ID doesn't touch. Mirrors the actual Birdy case, verified
// live: the file's Album/MusicBrainzAlbumID tags were correct even
// though its MusicBrainzRecordingID wasn't.
func TestScanRootFolderAutoRoutesDisagreeingDirectMatchToFolderConsensus(t *testing.T) {
	fs := newFolderTestServer()
	// The embedded recording ID resolves to a release group ("rg-mbid",
	// sampleRecording's own) that disagrees with the file's own
	// MusicBrainzReleaseGroupID tag below — the errDirectMatchInconsistent
	// trigger.
	fs.recordingLookups["rec-mismatched"] = sampleRecording("rec-mismatched", 0)
	// The file's own (correct) MusicBrainzAlbumID names a real,
	// resolvable release under a different release group — what folder
	// consensus should actually resolve it to.
	fs.releaseLookups["compilation-release"] = newTestAlbumRelease("compilation-release", "Skinny Love Comp", "Skinny Love")

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "song.flac", map[string]string{
		"ARTIST":                     "Birdy",
		"ALBUM":                      "Skinny Love Comp",
		"TITLE":                      "Skinny Love",
		"TRACKNUMBER":                "1",
		"MUSICBRAINZ_TRACKID":        "rec-mismatched",
		"MUSICBRAINZ_ALBUMID":        "compilation-release",
		"MUSICBRAINZ_RELEASEGROUPID": "rg-compilation-release", // does NOT match sampleRecording's "rg-mbid"
	})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1 — the declined file should auto-resolve via folder consensus, not sit unmatched", result.FilesMatched)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("matched = %+v, want 1", matched)
	}
	track, err := s.db.GetTrack(*matched[0].TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if track.Title != "Skinny Love" {
		t.Errorf("Track.Title = %q, want the release's own track title (resolved via folder consensus, not the bad recording lookup)", track.Title)
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

	unmatched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusUnmatched)
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

	files, err := s.db.ListTrackFilesByRootFolder(rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var trackTwoFile musiclibrary.TrackFile
	for _, f := range files {
		if filepath.Base(f.Path) == "02.flac" {
			trackTwoFile = f
		}
	}
	if trackTwoFile.TrackID == nil {
		t.Fatal("02.flac was not matched")
	}
	track, err := s.db.GetTrack(*trackTwoFile.TrackID)
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
	artist, err := s.db.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "rel-existing", "rg-existing", "Existing Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "rec-existing", "Already Matched", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	existingPath := buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Existing Album", "TITLE": "Already Matched", "TRACKNUMBER": "1"})
	existingTF, err := s.db.UpsertTrackFileByPath(rf.ID, existingPath, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(existingTF.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
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

// TestResolveExpectedReleaseSkipsSearchForGrabbedFiles is the regression
// test for the grab-provenance fast path: files stamped with a known
// expected_release_group_mbid (as internal/importer does before a
// completed grab's own scan runs) resolve straight to that release's
// best-by-file-count cached version, without ever calling release search
// — confirmed here by never configuring fs.releaseSearch at all, so the
// test would fail with "no candidates" if the shortcut weren't taken.
func TestResolveExpectedReleaseSkipsSearchForGrabbedFiles(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseLookups["rel-main"] = newTestAlbumRelease("rel-main", "Test Album", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	if err := s.db.ReplaceReleaseGroupVersions("rg-main", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-main", ReleaseMBID: "rel-main", Title: "Test Album", TrackCount: 2, IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}

	p1 := buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track One", "TRACKNUMBER": "1"})
	p2 := buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Test Album", "TITLE": "Track Two", "TRACKNUMBER": "2"})
	for _, p := range []string{p1, p2} {
		if err := s.db.SeedExpectedReleaseGroup(rf.ID, p, "rg-main"); err != nil {
			t.Fatalf("SeedExpectedReleaseGroup(%s): %v", p, err)
		}
	}

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if fs.countOf("release-search") != 0 {
		t.Errorf("release-search count = %d, want 0 — the grab-provenance shortcut should skip it entirely", fs.countOf("release-search"))
	}
	if result.FilesMatched != 2 {
		t.Fatalf("FilesMatched = %d, want 2 (result=%+v)", result.FilesMatched, result)
	}

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "artist-mbid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].MBID != "rel-main" {
		t.Fatalf("albums = %+v, want exactly rel-main", albums)
	}
}

// TestResolveExpectedReleaseSafetyGateRefusesMismatchedTags is the
// regression test for the safety concern raised while designing this
// feature: a grab whose actual content doesn't match what was searched
// for (a bad/mislabeled download) must not be force-bound to the wrong
// release just because CantiNode expected something else. When the
// folder's own tags positively name an album that doesn't look like the
// expected release group's cached title, the shortcut refuses itself and
// falls through to the normal search-based path — proven here by
// asserting release-search WAS called, and that the file ends up matched
// to what its own tags actually say rather than the (wrong) expectation.
func TestResolveExpectedReleaseSafetyGateRefusesMismatchedTags(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-actual", Title: "What This Really Is", Score: 100, TrackCount: 2,
			ArtistCredit: []mbArtistCredit{{Name: "Test Artist", Artist: mbArtistRef{ID: "artist-mbid", Name: "Test Artist"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-actual", Title: "What This Really Is", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-actual"] = newTestAlbumRelease("rel-actual", "What This Really Is", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)
	if err := s.db.ReplaceReleaseGroupVersions("rg-expected", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-expected", ReleaseMBID: "rel-expected", Title: "The Expected Album", TrackCount: 2, IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}

	// Two files (not one) — resolveFolderRelease's own pre-existing
	// "nothing to disambiguate with a single file" shortcut skips release
	// search entirely regardless of expectations, which would otherwise
	// mask what this test is actually checking.
	p1 := buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "What This Really Is", "TITLE": "Track One", "TRACKNUMBER": "1"})
	p2 := buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "What This Really Is", "TITLE": "Track Two", "TRACKNUMBER": "2"})
	for _, p := range []string{p1, p2} {
		if err := s.db.SeedExpectedReleaseGroup(rf.ID, p, "rg-expected"); err != nil {
			t.Fatalf("SeedExpectedReleaseGroup(%s): %v", p, err)
		}
	}

	if _, err := s.ScanRootFolder(t.Context(), rf); err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if fs.countOf("release-search") == 0 {
		t.Error("release-search count = 0, want at least 1 — the safety gate should have refused the mismatched expectation and fallen through to search")
	}

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "artist-mbid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].MBID != "rel-actual" {
		t.Fatalf("albums = %+v, want the file matched to what it actually is (rel-actual), not the wrong expectation", albums)
	}
}

// TestResolveExpectedReleaseSafetyGateRefusesInternallyDisagreeingTags
// covers the case TestResolveExpectedReleaseSafetyGateRefusesMismatchedTags
// doesn't: a folder whose own files disagree with EACH OTHER about what
// album they are (not just with the expectation). folderTagConsensus alone
// treats "tags disagree with each other" and "no album tag present at
// all" identically (ok=false either way) — without albumTagsDisagree
// telling these two cases apart, this internally-inconsistent folder would
// have been treated as "nothing to contradict the expectation" and the
// grab-provenance shortcut would have proceeded uncontested, force-binding
// both files to the (wrong) expected release group.
//
// Once the shortcut is refused here, folderTagConsensus's own pre-existing
// album-disagreement check also fails the whole-folder search path (see
// TestScanRootFolderInconsistentTagsFallsBackToPerFileMatching), so
// resolution degrades all the way to independent per-file fuzzy matching —
// proven by
// asserting release-search was never called but recording-search (the
// per-file fallback) was, and that neither file ends up bound to the
// expected release group's MBID.
func TestResolveExpectedReleaseSafetyGateRefusesInternallyDisagreeingTags(t *testing.T) {
	fs := newFolderTestServer()
	fs.recordingSearch = []mbRecording{sampleRecording("fallback-rec", 100)}

	s, rf := newFolderTestScanner(t, fs)
	if err := s.db.ReplaceReleaseGroupVersions("rg-expected", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-expected", ReleaseMBID: "rel-expected", Title: "The Expected Album", TrackCount: 2, IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}

	// The two files' own Album tags disagree with EACH OTHER, not just
	// with the expectation — a red flag on its own regardless of what was
	// expected.
	p1 := buildFLACFile(t, rf.Path, "01.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Album A", "TITLE": "Track One", "TRACKNUMBER": "1"})
	p2 := buildFLACFile(t, rf.Path, "02.flac", map[string]string{"ARTIST": "Test Artist", "ALBUM": "Album B", "TITLE": "Track Two", "TRACKNUMBER": "2"})
	for _, p := range []string{p1, p2} {
		if err := s.db.SeedExpectedReleaseGroup(rf.ID, p, "rg-expected"); err != nil {
			t.Fatalf("SeedExpectedReleaseGroup(%s): %v", p, err)
		}
	}

	if _, err := s.ScanRootFolder(t.Context(), rf); err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if fs.countOf("release-search") != 0 {
		t.Errorf("release-search count = %d, want 0 — a folder that can't even agree on its own album isn't a whole-folder search candidate either", fs.countOf("release-search"))
	}
	if fs.countOf("recording-search") == 0 {
		t.Error("recording-search count = 0, want at least 1 — should have degraded to independent per-file fuzzy matching")
	}

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "artist-mbid"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range albums {
		if a.MBID == "rel-expected" {
			t.Errorf("albums = %+v, must not include the expected release group's MBID — internally-disagreeing tags should never force-bind to the expectation", albums)
		}
	}
}

func TestPickBestVersionByFileCount(t *testing.T) {
	versions := []musiclibrary.ReleaseGroupVersion{
		{ReleaseMBID: "rel-10", TrackCount: 10},
		{ReleaseMBID: "rel-12", TrackCount: 12, IsRepresentative: true},
		{ReleaseMBID: "rel-15", TrackCount: 15},
		{ReleaseMBID: "rel-unknown", TrackCount: 0}, // no usable track count — never a candidate
	}
	if got := pickBestVersionByFileCount(versions, 12); got == nil || got.ReleaseMBID != "rel-12" {
		t.Errorf("exact match: got %+v, want rel-12", got)
	}
	if got := pickBestVersionByFileCount(versions, 11); got == nil || got.ReleaseMBID != "rel-12" {
		t.Errorf("closest match (tie 10 vs 12, both diff 1): got %+v, want the tie broken toward representative rel-12", got)
	}
	if got := pickBestVersionByFileCount(nil, 5); got != nil {
		t.Errorf("empty versions: got %+v, want nil", got)
	}
	if got := pickBestVersionByFileCount([]musiclibrary.ReleaseGroupVersion{{ReleaseMBID: "x", TrackCount: 0}}, 5); got != nil {
		t.Errorf("only unusable versions: got %+v, want nil", got)
	}
}

func TestJoinArtistCredit(t *testing.T) {
	cases := []struct {
		name string
		in   []musicbrainz.ArtistCredit
		want string
	}{
		{"empty", nil, ""},
		{"single", []musicbrainz.ArtistCredit{{Name: "Phil Collins"}}, "Phil Collins"},
		{"multiple", []musicbrainz.ArtistCredit{{Name: "Artist A"}, {Name: "Artist B"}}, "Artist A, Artist B"},
	}
	for _, c := range cases {
		if got := joinArtistCredit(c.in); got != c.want {
			t.Errorf("%s: joinArtistCredit(%+v) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestMatchEntriesToReleaseStoresPerTrackArtistCredit is the regression
// test for the VA compilation gap: each track of a resolved release must
// get its own real performing-artist credit stored — not the release's
// own "Various Artists" credit repeated on every track, which is what
// recordingForReleaseTrack's ArtistCredit field (used for artist/album
// assignment) alone would give if applyMatch didn't receive the track's
// real credit separately.
func TestMatchEntriesToReleaseStoresPerTrackArtistCredit(t *testing.T) {
	fs := newFolderTestServer()
	release := mbReleaseWithTracklist{
		ID:    "rel-va",
		Title: "Various Artists Comp",
		ArtistCredit: []mbArtistCredit{
			{Name: "Various Artists", Artist: mbArtistRef{ID: "va-mbid", Name: "Various Artists"}},
		},
		ReleaseGroup: mbReleaseGroup{ID: "rg-va", Title: "Various Artists Comp", PrimaryType: "Album"},
		Media: []mbMedium{{Format: "CD", Position: 1, TrackCount: 2, Tracks: []mbReleaseTrack{
			{Position: 1, Title: "Track One", Recording: mbTrackRecording{
				ID: "rec-1", Title: "Track One", Length: 200000,
				ArtistCredit: []mbArtistCredit{{Name: "Phil Collins", Artist: mbArtistRef{ID: "phil-mbid", Name: "Phil Collins"}}},
			}},
			{Position: 2, Title: "Track Two", Recording: mbTrackRecording{
				ID: "rec-2", Title: "Track Two", Length: 200000,
				ArtistCredit: []mbArtistCredit{{Name: "Duran Duran", Artist: mbArtistRef{ID: "duran-mbid", Name: "Duran Duran"}}},
			}},
		}}},
	}
	fs.releaseLookups["rel-va"] = release

	s, rf := newFolderTestScanner(t, fs)
	buildFLACFile(t, rf.Path, "01.flac", map[string]string{"MUSICBRAINZ_ALBUMID": "rel-va", "TITLE": "Track One", "TRACKNUMBER": "1", "ARTIST": "Various Artists", "ALBUM": "Various Artists Comp"})

	// Route straight through matchFolder's embedded-release-MBID fast path
	// (the file names the release directly) so this test exercises
	// matchEntriesToRelease deterministically without depending on search
	// scoring.
	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1 (result=%+v)", result.FilesMatched, result)
	}

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "va-mbid"))
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums = %+v, err %v, want exactly 1 under Various Artists", albums, err)
	}
	tracks, err := s.db.ListTracksByAlbum(albums[0].ID)
	if err != nil || len(tracks) != 1 {
		t.Fatalf("tracks = %+v, err %v, want exactly 1", tracks, err)
	}
	// Track One's own credit is "Phil Collins" — deliberately different
	// from the release's own "Various Artists" credit, so a stored value
	// matching it (not "Various Artists") proves the real per-track
	// credit was used for display, while the album itself still correctly
	// filed under Various Artists (asserted above).
	if tracks[0].ArtistCredit != "Phil Collins" {
		t.Errorf("track ArtistCredit = %q, want %q (the track's own real credit, not the album's)", tracks[0].ArtistCredit, "Phil Collins")
	}
}

func mustArtistID(t *testing.T, s *Scanner, mbid string) int64 {
	t.Helper()
	artist, err := s.db.GetOrCreateArtist(mbid, "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	return artist.ID
}

// TestRecordingForReleaseTrackPreservesRelations is the regression test for
// a real bug caught during composer support's own implementation: the
// synthesized Recording recordingForReleaseTrack builds for applyMatch
// copied ID/Title/Length/Releases from ft.Recording but silently dropped
// its Relations, so a track matched through the folder-consensus path
// (matchEntriesToRelease, backed by LookupReleaseWithTracklist — the one
// path expected to have real composer data for every track, not just the
// direct-match single-recording path) would always resolve an empty
// Composer despite the underlying MusicBrainz response actually carrying
// the work-relation data.
func TestRecordingForReleaseTrackPreservesRelations(t *testing.T) {
	ft := flatTrack{
		disc: 1,
		ReleaseTrack: musicbrainz.ReleaseTrack{
			Position: 1,
			Title:    "Hallelujah",
			Recording: musicbrainz.Recording{
				ID:     "rec-1",
				Title:  "Hallelujah",
				Length: 240000,
				Relations: []musicbrainz.Relation{
					{
						Type: "performance", TargetType: "work",
						Work: &musicbrainz.Work{Relations: []musicbrainz.Relation{
							{Type: "composer", TargetType: "artist", Artist: &musicbrainz.ArtistRef{ID: "cohen", Name: "Leonard Cohen"}},
						}},
					},
				},
			},
		},
	}
	release := &musicbrainz.ReleaseWithTracklist{ID: "release-1"}

	got := recordingForReleaseTrack(ft, release)
	if got.Composer() != "Leonard Cohen" {
		t.Errorf("recordingForReleaseTrack(...).Composer() = %q, want %q — Relations must survive the synthesized Recording", got.Composer(), "Leonard Cohen")
	}
}

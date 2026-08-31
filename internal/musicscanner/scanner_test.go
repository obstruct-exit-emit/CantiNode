package musicscanner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// TestScanRootFolderSkipsTagReadForUnchangedMatchedFile is the regression
// test for a real scan-speed gap: an already-matched file had its tags
// re-read from disk on *every* scan regardless of whether it had changed
// since the last one — real, avoidable I/O at scale, worse on a
// network-mounted library. A matched file's own on-disk size is used as
// a cheap freshness check (a stat(), not a full read) — unchanged size
// skips the tag re-read entirely; a genuinely different size (a real
// re-tag/re-encode/replacement) still gets read fresh, and an unmatched
// file is always read regardless.
//
// Directly detects whether the tag read actually happened rather than
// asserting on internal state: the file's on-disk *content* is corrupted
// to something tagreader can't parse (real tag reading would surface a
// "read tags" error) while carefully controlling whether its *size*
// changes — same size proves the skip fired (no error, stale-but-correct
// data survives unread); different size proves the fresh read still
// happened (correctly errors on the now-garbage content).
func TestScanRootFolderSkipsTagReadForUnchangedMatchedFile(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	artist, err := s.db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}

	seedMatched := func(name string, initialContent []byte) (path string, trackID int64) {
		t.Helper()
		track, err := s.db.GetOrCreateTrack(album.ID, name+"-mbid", name, 1, 1, 100_000, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(rf.Path, name+".flac")
		if err := os.WriteFile(path, initialContent, 0o644); err != nil {
			t.Fatal(err)
		}
		tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, int64(len(initialContent)), "flac", 0, 0, "{}")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
			t.Fatal(err)
		}
		return path, track.ID
	}

	// unchangedPath: corrupted content, but kept at its original size —
	// the freshness check should skip re-reading it, so the garbage
	// content is never actually parsed.
	unchangedPath, _ := seedMatched("Unchanged", []byte("valid-enough-initial-bytes"))
	if err := os.WriteFile(unchangedPath, []byte(strings.Repeat("X", len("valid-enough-initial-bytes"))), 0o644); err != nil {
		t.Fatal(err)
	}

	// changedPath: corrupted content at a genuinely different size — the
	// freshness check must NOT skip this one; it should be read fresh and
	// surface a real "read tags" error on the garbage.
	changedPath, _ := seedMatched("Changed", []byte("valid-enough-initial-bytes"))
	if err := os.WriteFile(changedPath, []byte(strings.Repeat("Y", 5)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}

	// Matched against the full filename, not a bare "Unchanged"/"Changed"
	// substring — t.TempDir() embeds this very test's own function name in
	// every path it hands out, and that name itself contains "Unchanged",
	// which would otherwise make either check match any error at all.
	for _, e := range result.Errors {
		if strings.Contains(e, "Unchanged.flac") {
			t.Errorf("unchanged (same-size) file's tags were re-read despite being unchanged: %v", result.Errors)
		}
	}
	foundChangedErr := false
	for _, e := range result.Errors {
		if strings.Contains(e, "/Changed.flac") {
			foundChangedErr = true
		}
	}
	if !foundChangedErr {
		t.Errorf("changed (different-size) file should have been read fresh and errored on its now-garbage content, errors = %v", result.Errors)
	}
}

// TestScanAllSerializesConcurrentCalls is the regression test for a real
// gap: ScanAll had no guard against running concurrently with itself.
// Nothing stopped the periodic importer sweep's own post-import ScanAll
// call (internal/importer.importGrab) from overlapping with a manual
// "Import now"/"Scan library" trigger's ScanAll call on the very same
// Scanner — two full-library scans racing on the same DB, matching (and
// writing) the same files at once. Found live as "downloading albums
// sometimes don't show up in Activity": a completed grab's post-copy scan
// interfered with by a second concurrent scan can come back having matched
// nothing, so importGrab reverts the album back to "wanted" even though
// its files really were copied in — intermittent, since it only bites
// when two scans' timing happens to overlap.
//
// Held externally here rather than actually racing two real scans against
// each other: real scans finish fast enough on a tiny test fixture that a
// timing-based race would be flaky. Locking scanMu directly and asserting
// ScanAll blocks until it's released proves the same thing deterministically.
func TestScanAllSerializesConcurrentCalls(t *testing.T) {
	s, _ := newTestScanner(t, nil, nil)

	s.scanMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.ScanAll(t.Context()); err != nil {
			t.Errorf("ScanAll: %v", err)
		}
	}()

	select {
	case <-done:
		t.Fatal("ScanAll returned while scanMu was still held externally — concurrent scans aren't serialized")
	case <-time.After(200 * time.Millisecond):
		// Still blocked, as expected.
	}

	s.scanMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ScanAll did not proceed after scanMu was released")
	}
}

// TestScanRootFolderSkipsPruneWhenWalkHitsUnreadableSubfolder is the
// regression test for a real data-loss gap: ScanRootFolder's own doc
// comment already explains why a root folder that's entirely inaccessible
// (a network mount briefly down) must never reach DeleteTrackFilesMissing
// with an empty seenPaths — but that same reasoning was never extended to
// a transient failure on just *one subfolder* partway through an otherwise
// healthy walk (an AV lock, a brief reconnect on one nested share). A
// directory WalkDir can't read is never descended into, so every file
// underneath it silently never makes it into seenPaths either — and
// DeleteTrackFilesMissing treats "not in seenPaths" as "gone from disk,"
// permanently deleting the track_files row (match status, organized path,
// any manual review) for a file that's still genuinely sitting right
// there, the moment any subfolder has a one-off read hiccup during a scan.
func TestScanRootFolderSkipsPruneWhenWalkHitsUnreadableSubfolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits aren't enforced the same way on Windows")
	}
	s, rf := setupOrganizeScanner(t)
	_, path := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")

	blockedDir := filepath.Join(rf.Path, "Boards of Canada")
	if err := os.Chmod(blockedDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o755) })

	result, err := s.ScanRootFolder(context.Background(), rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if result.FilesRemoved != 0 {
		t.Errorf("FilesRemoved = %d, want 0 — the file is still really there, the scan just couldn't confirm it this pass", result.FilesRemoved)
	}
	foundSkipNotice := false
	for _, e := range result.Errors {
		if strings.Contains(e, "skipped removing missing files") {
			foundSkipNotice = true
		}
	}
	if !foundSkipNotice {
		t.Errorf("Errors = %v, want a notice that pruning was skipped this pass", result.Errors)
	}

	tf, err := s.db.GetTrackFileByPath(path)
	if err != nil {
		t.Fatalf("track file row for %s should still exist after a transient walk error, got: %v", path, err)
	}
	if tf.MatchStatus != musiclibrary.StatusMatched {
		t.Errorf("MatchStatus = %v, want the pre-existing match to have survived untouched", tf.MatchStatus)
	}
}

// TestScanAlbumFolderSerializesAgainstScanAll is the regression test for a
// gap left behind by e2097f8 (which serialized ScanAll's own three call
// sites against each other via scanMu, but never touched ScanAlbumFolder):
// the album page's own "Scan files" action upserts/matches/organizes
// through the exact same track_files rows and on-disk moves ScanAll does,
// so it can race with a concurrent ScanAll (e.g. the periodic importer
// sweep) exactly the way two concurrent ScanAlls used to — the same
// "downloaded albums sometimes vanish from Activity" symptom, just
// triggered by an album scan instead of a second full-library scan.
//
// Held externally here for the same reason TestScanAllSerializesConcurrentCalls
// does: a real race against a tiny test fixture finishes too fast to
// reliably reproduce the timing-dependent original bug.
func TestScanAlbumFolderSerializesAgainstScanAll(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	album, _ := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")

	s.scanMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.ScanAlbumFolder(t.Context(), album.ID); err != nil {
			t.Errorf("ScanAlbumFolder: %v", err)
		}
	}()

	select {
	case <-done:
		t.Fatal("ScanAlbumFolder returned while scanMu was still held externally — it isn't serialized against ScanAll")
	case <-time.After(200 * time.Millisecond):
		// Still blocked, as expected.
	}

	s.scanMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ScanAlbumFolder did not proceed after scanMu was released")
	}
}

// TestCommonAncestorDir exercises the helper ScanAlbumFolder uses to find
// one walk root covering every existing track file's directory — the
// single-directory case must stay a no-op (an ordinary single-disc album
// behaves exactly as before this fix), and a multi-disc layout must reduce
// to the shared parent, not a meaningless partial-string overlap.
func TestCommonAncestorDir(t *testing.T) {
	cases := []struct {
		name string
		dirs []string
		want string
	}{
		{
			name: "single directory",
			dirs: []string{filepath.FromSlash("/music/Pink Floyd/The Wall")},
			want: filepath.FromSlash("/music/Pink Floyd/The Wall"),
		},
		{
			name: "same directory repeated",
			dirs: []string{
				filepath.FromSlash("/music/Pink Floyd/The Wall"),
				filepath.FromSlash("/music/Pink Floyd/The Wall"),
			},
			want: filepath.FromSlash("/music/Pink Floyd/The Wall"),
		},
		{
			name: "two disc subfolders share the album folder",
			dirs: []string{
				filepath.FromSlash("/music/Avantasia/Moonglow (2CD)/CD1"),
				filepath.FromSlash("/music/Avantasia/Moonglow (2CD)/CD2"),
			},
			want: filepath.FromSlash("/music/Avantasia/Moonglow (2CD)"),
		},
		{
			name: "three disc subfolders",
			dirs: []string{
				filepath.FromSlash("/music/Box Set/CD1"),
				filepath.FromSlash("/music/Box Set/CD2"),
				filepath.FromSlash("/music/Box Set/CD3"),
			},
			want: filepath.FromSlash("/music/Box Set"),
		},
		{
			name: "album titles sharing a string prefix must not fool component-wise comparison",
			dirs: []string{
				filepath.FromSlash("/music/Album"),
				filepath.FromSlash("/music/Album2"),
			},
			want: filepath.FromSlash("/music"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commonAncestorDir(c.dirs); got != c.want {
				t.Errorf("commonAncestorDir(%v) = %q, want %q", c.dirs, got, c.want)
			}
		})
	}
}

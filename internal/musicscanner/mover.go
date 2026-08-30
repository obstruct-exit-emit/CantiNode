package musicscanner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ArtistMove is one track file's planned (or applied) cross-root-folder
// relocation — the move-artist counterpart to organizer.go's RenameMove.
// SizeBytes lets a preview show the total size about to move without a
// second round trip.
type ArtistMove struct {
	TrackFileID int64  `json:"fileId"`
	From        string `json:"from"`
	To          string `json:"to"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// PlanMoveArtist previews the moves MoveArtist would make: every one of
// artistID's own track files not already on destRootFolderID, each kept
// at the same path *relative to its own root folder* — a pure relocation,
// not a re-organize. Whatever structure a file already has (organized
// per the naming template or not) survives unchanged under the new root;
// Organize is a separate, deliberately distinct action for anyone who
// also wants the naming template reapplied. A file already on the
// destination root is left out entirely, the same way an
// already-correctly-named file is left out of PlanOrganizeArtist.
//
// Only covers files ListTrackFilesByArtist can see — matched files linked
// through tracks/albums to this artist. A file still sitting unmatched in
// the same folder tree (nothing has associated it with any artist yet —
// see the unmatched-files review page) is not included and is left behind
// on the old root folder; there is no artist-scoped query that could find
// it, since being unmatched is exactly what "not yet linked to an artist"
// means. Match or otherwise resolve those first if you want a folder to
// relocate in full. PlanOrganizeArtist has the identical limitation and
// says so for the same reason.
func (s *Scanner) PlanMoveArtist(artistID, destRootFolderID int64) ([]ArtistMove, error) {
	dest, err := s.db.GetRootFolder(destRootFolderID)
	if err != nil {
		return nil, fmt.Errorf("get destination root folder: %w", err)
	}

	files, err := s.db.ListTrackFilesByArtist(artistID)
	if err != nil {
		return nil, fmt.Errorf("list track files by artist: %w", err)
	}

	// Cache each source root folder's path — an artist's files can
	// already be spread across more than one root folder (e.g. a partial
	// move from a previous attempt, or files added under different roots
	// over time), and every distinct one needs its own relative-path base.
	srcRoots := map[int64]string{}
	moves := []ArtistMove{}
	for _, tf := range files {
		if tf.RootFolderID == destRootFolderID {
			continue
		}
		srcPath, ok := srcRoots[tf.RootFolderID]
		if !ok {
			srcRoot, err := s.db.GetRootFolder(tf.RootFolderID)
			if err != nil {
				return nil, fmt.Errorf("get source root folder for %s: %w", tf.Path, err)
			}
			srcPath = srcRoot.Path
			srcRoots[tf.RootFolderID] = srcPath
		}
		relPath, err := filepath.Rel(srcPath, tf.Path)
		if err != nil {
			return nil, fmt.Errorf("relativize %s under %s: %w", tf.Path, srcPath, err)
		}
		moves = append(moves, ArtistMove{TrackFileID: tf.ID, From: tf.Path, To: filepath.Join(dest.Path, relPath), SizeBytes: tf.SizeBytes})
	}
	return moves, nil
}

// MoveArtist applies PlanMoveArtist's plan, one file at a time. A
// failure moving one file is recorded in errs and does not stop the
// rest — the same non-aborting convention applyOrganizePlan and a whole
// scan pass's ScanResult.Errors both already use. ctx being canceled
// (e.g. the server shutting down mid-move) stops before starting any
// further file, but never mid-file — a single file's own copy-verify-
// delete-record sequence always runs to completion or reports a clean
// failure, so the database is never left disagreeing with disk about
// where a file actually lives, no matter when this stops.
func (s *Scanner) MoveArtist(ctx context.Context, artistID, destRootFolderID int64) (moved []ArtistMove, errs []string, err error) {
	plan, err := s.PlanMoveArtist(artistID, destRootFolderID)
	if err != nil {
		return nil, nil, err
	}

	moved = []ArtistMove{}
	errs = []string{}
	for _, m := range plan {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Sprintf("%s: move canceled before this file started", m.From))
			continue
		}
		if err := s.moveTrackFile(m.TrackFileID, destRootFolderID, m.To); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.From, err))
			continue
		}
		moved = append(moved, m)
	}
	return moved, errs, nil
}

// moveTrackFile relocates one file: copy to a temp name beside newPath,
// verify the copy's size matches the source, rename into place (atomic
// within the destination directory — nothing ever observes a partially-
// written file at newPath itself), record the new root_folder_id/path,
// then delete the original. Each step only proceeds once the previous
// one is confirmed, so the worst any failure leaves behind is a stray
// .partial file (copy step) or a harmless duplicate on the old root
// (delete step, logged, never fatal) — the source file and an accurate
// database are never both at risk from the same failure.
func (s *Scanner) moveTrackFile(trackFileID, destRootFolderID int64, newPath string) error {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return fmt.Errorf("get track file: %w", err)
	}
	if tf.RootFolderID == destRootFolderID {
		return nil // already moved — safe to re-run a plan that partially succeeded
	}
	srcRoot, err := s.db.GetRootFolder(tf.RootFolderID)
	if err != nil {
		return fmt.Errorf("get source root folder: %w", err)
	}

	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("destination already exists: %s", newPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination %s: %w", newPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	tmpPath := newPath + ".cantinode-moving"
	if err := copyFileVerified(tf.Path, tmpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("copy to new root: %w", err)
	}
	if err := os.Rename(tmpPath, newPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("finalize %s: %w", newPath, err)
	}

	if err := s.db.SetTrackFileLocation(trackFileID, destRootFolderID, newPath); err != nil {
		// The copy already landed at newPath, but the database still
		// (now incorrectly) points at tf.Path. Left there, every retry
		// would hit this same function's very first check above
		// ("destination already exists") forever, since nothing about the
		// stored state ever changed — a permanently stuck file with no
		// path forward except manual disk cleanup. Moving it back restores
		// the exact pre-move state instead, so a retry starts clean. Only
		// if that ALSO fails (rare — e.g. the source directory disappeared
		// out from under this call) does the file end up stuck at newPath
		// with a stale database row, which the error message below spells
		// out precisely so it can be fixed by hand.
		if rerr := os.Rename(newPath, tf.Path); rerr != nil {
			return fmt.Errorf("record new location failed (%v), and restoring the original also failed (%v) — "+
				"the file is now at %s but the database still says %s", err, rerr, newPath, tf.Path)
		}
		return fmt.Errorf("record new location, rolled back to original location: %w", err)
	}

	if err := os.Remove(tf.Path); err != nil {
		// The database is already correctly pointing at newPath — a
		// leftover stale duplicate at the old path is untidy, not data
		// loss, unlike any failure above this point.
		s.logger.Warn("mover: removing old file after a successful move failed, leaving a stale duplicate on disk",
			"old_path", tf.Path, "new_path", newPath, "error", err)
	}
	removeEmptyParents(filepath.Dir(tf.Path), srcRoot.Path)
	s.notifyPlexPaths(tf.Path, newPath)
	return nil
}

// copyFileVerified copies src to dst (io.Copy, not os.Rename — the whole
// reason this exists is that src and dst can be on different
// filesystems/drives) and confirms the written file's size matches the
// source's before returning success, a cheap sanity check that a short
// write or a mid-copy disk error doesn't get treated as a clean move.
func copyFileVerified(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	written, err := io.Copy(out, in)
	if err != nil {
		out.Close()
		return fmt.Errorf("copy to %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	if written != srcInfo.Size() {
		return fmt.Errorf("copied %d bytes, want %d (source: %s)", written, srcInfo.Size(), src)
	}
	return nil
}

// removeEmptyParents deletes dir, then each ancestor in turn for as long
// as each one is genuinely empty, stopping at (never removing) boundary
// itself — cleans up the leftover husk of an artist's old folder
// structure after every file under it has moved elsewhere. Any failure
// (not empty, permission denied, ...) is treated as the natural place to
// stop, not an error: this is tidiness, never a required step, and the
// caller's own move has already fully succeeded by the time this runs.
func removeEmptyParents(dir, boundary string) {
	boundary = filepath.Clean(boundary)
	dir = filepath.Clean(dir)
	for dir != boundary && dir != "." && dir != string(filepath.Separator) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

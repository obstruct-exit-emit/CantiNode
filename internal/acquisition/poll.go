package acquisition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cantinode/cantinode/internal/acervinode"
	"github.com/cantinode/cantinode/internal/database"
)

// PollResult summarizes one PollDownloads pass, for logging.
type PollResult struct {
	Checked  int
	Imported int
	Errored  int
}

// PollDownloads checks every in-flight download against AcerviNode and
// imports (or errors out) whichever ones it reports done — the
// acquisition side's equivalent of internal/scanner's own scan loop. A
// no-op, not an error, if AcerviNode isn't configured yet.
func (s *Service) PollDownloads(ctx context.Context) (PollResult, error) {
	var result PollResult

	av := s.getAcervi()
	if av == nil {
		return result, nil
	}

	downloads, err := s.db.ListDownloadsByStatus(ctx, database.DownloadStatusDownloading)
	if err != nil {
		return result, fmt.Errorf("list in-flight downloads: %w", err)
	}

	for _, d := range downloads {
		result.Checked++
		imported, err := s.pollOne(ctx, av, d)
		if err != nil {
			result.Errored++
			s.logger.Error("acquisition: poll download failed", "download_id", d.ID, "error", err)
			continue
		}
		if imported {
			result.Imported++
		}
	}
	return result, nil
}

// pollOne checks one download's status and acts on it, reporting
// whether it was imported this pass. d is passed (and stays) by value —
// its own Status field is never mutated in place; the database is the
// only source of truth once this returns.
func (s *Service) pollOne(ctx context.Context, av *acervinode.Client, d database.Download) (imported bool, err error) {
	var status *acervinode.Status
	switch d.Protocol {
	case database.ProtocolTorrent:
		status, err = av.GetTorrentStatus(ctx, d.ClientID)
	case database.ProtocolUsenet:
		status, err = av.GetUsenetStatus(ctx, d.ClientID)
	default:
		return false, fmt.Errorf("unknown protocol %q", d.Protocol)
	}

	if errors.Is(err, acervinode.ErrNotFound) {
		return false, s.failDownload(ctx, d, "AcerviNode no longer has this download (removed directly there?)")
	}
	if err != nil {
		return false, fmt.Errorf("get status from AcerviNode: %w", err)
	}

	switch status.State {
	case acervinode.StateDownloading:
		return false, nil // still in progress, nothing to do this pass

	case acervinode.StateError:
		msg := status.ErrorMessage
		if msg == "" {
			msg = "AcerviNode reported this download as failed"
		}
		return false, s.failDownload(ctx, d, msg)

	case acervinode.StateCompleted:
		now := time.Now().UTC()
		if err := s.db.SetDownloadCompleted(ctx, d.ID, now); err != nil {
			return false, fmt.Errorf("record download completed: %w", err)
		}
		if err := s.importDownload(ctx, d, status.LocalPath); err != nil {
			return false, err
		}
		return true, nil

	default:
		return false, fmt.Errorf("unrecognized status state %q", status.State)
	}
}

// failDownload records a download as errored and reverts its wanted
// album back to "wanted" — a failed release (dead torrent, missing
// article) shouldn't leave the album stuck; the user can search again
// and try a different one, the same as if it had never been grabbed.
func (s *Service) failDownload(ctx context.Context, d database.Download, message string) error {
	if err := s.db.SetDownloadError(ctx, d.ID, message); err != nil {
		return fmt.Errorf("record download error: %w", err)
	}
	if err := s.db.SetWantedAlbumStatus(ctx, d.WantedAlbumID, database.WantedStatusWanted); err != nil {
		return fmt.Errorf("revert wanted album status: %w", err)
	}
	return nil
}

// importDownload copies a completed download's files from localPath
// (AcerviNode's own local disk — requires CantiNode and AcerviNode to
// share a filesystem view, the same assumption any *arr app's download
// client integration already makes) into d's target root folder, under
// a subfolder keyed by the download's own ID (not its title — always
// filesystem-safe with zero sanitizing, and directly traceable back to
// this row for debugging). internal/scanner then picks the copied files
// up exactly like any other file dropped into a root folder: matched via
// their own embedded tags, organized on the normal schedule.
//
// A copy, not a move: AcerviNode retains its own copy under its own
// retention/cleanup policy, which CantiNode has no business overriding.
func (s *Service) importDownload(ctx context.Context, d database.Download, localPath string) error {
	rootFolder, err := s.db.GetRootFolder(ctx, d.RootFolderID)
	if err != nil {
		return s.failDownload(ctx, d, fmt.Sprintf("import target root folder is gone: %v", err))
	}

	dest := filepath.Join(rootFolder.Path, "_incoming", "download-"+strconv.FormatInt(d.ID, 10))
	if err := copyTree(localPath, dest); err != nil {
		return s.failDownload(ctx, d, fmt.Sprintf("copy from AcerviNode failed: %v", err))
	}

	if _, err := s.scanner.ScanRootFolder(ctx, *rootFolder); err != nil {
		s.logger.Warn("acquisition: post-import scan failed, next scheduled scan will still pick these files up", "download_id", d.ID, "error", err)
	}

	now := time.Now().UTC()
	if err := s.db.SetDownloadImported(ctx, d.ID, now); err != nil {
		return fmt.Errorf("record download imported: %w", err)
	}
	if err := s.db.SetWantedAlbumStatus(ctx, d.WantedAlbumID, database.WantedStatusDownloaded); err != nil {
		return fmt.Errorf("set wanted album downloaded: %w", err)
	}
	return nil
}

// copyTree copies src (a file or a directory, recursively) into dstDir,
// preserving relative structure for a directory.
func copyTree(src, dstDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", src, err)
	}
	if !info.IsDir() {
		return copyFile(src, filepath.Join(dstDir, filepath.Base(src)))
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy to %s: %w", dst, err)
	}
	return out.Close()
}

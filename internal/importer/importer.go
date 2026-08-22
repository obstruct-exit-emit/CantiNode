// Package importer polls in-flight grabs against their download clients
// and, once one completes, copies its files into the music library and
// scans them in — the automatic half of CantiNode's acquisition pipeline.
// Grabbing itself is always a manual, human choice (see internal/download);
// this is what happens after, so a finished download doesn't just sit in
// the download client's folder until someone remembers to go get it.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// PollInterval is how often in-flight grabs are checked against their
// download clients — independent of (and much shorter than) a library
// scan, since a user watching a grab wants responsive feedback. Not
// currently exposed as a setting: a fixed, reasonable default rather than
// one more knob.
const PollInterval = 2 * time.Minute

// Service ties the download queue, the configured root folders, and the
// music scanner together.
type Service struct {
	downloads *download.Service
	scanner   *musicscanner.Scanner
	music     *musiclibrary.Store
	cfg       *config.Config
	logger    *slog.Logger
}

func New(downloads *download.Service, scanner *musicscanner.Scanner, music *musiclibrary.Store, cfg *config.Config) *Service {
	return &Service{downloads: downloads, scanner: scanner, music: music, cfg: cfg, logger: slog.Default()}
}

// RunPeriodic polls immediately (so a fresh start doesn't wait a full
// interval to catch up on anything already finished), then again every
// interval until ctx is canceled. interval <= 0 uses PollInterval.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = PollInterval
	}
	s.PollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollOnce(ctx)
		}
	}
}

// PollResult summarizes one PollOnce pass, for logging/testing.
type PollResult struct {
	Checked  int
	Imported int
	Failed   int
}

// grabVanishedGrace bounds how soon after a grab PollOnce is willing to
// conclude "not in the queue" means gone for good. Found live: a debrid
// bridge (an NZB grab through TorBox's SABnzbd-compatible endpoint) can
// take longer than one PollInterval to actually list an item it already
// accepted — GrabRelease's own synchronous wait on the debrid service
// (see download.downloadTimeout's doc comment) only covers the *submit*
// call, not how soon the bridge's own queue/history reflects it afterward.
// A grab still younger than this was, twice, wrongly failed and reverted
// to "wanted" within a minute of being sent, on a completely healthy
// release that the very next poll would have found — repeating on
// consecutive days once autosearch re-grabbed the same still-wanted album,
// with the actually-completed file just sitting unimported in the
// download folder each time. Comfortably longer than PollInterval so a
// slow bridge gets several looks before being given up on; a grab that's
// genuinely gone (removed in the client, lost to a client restart) still
// fails, just not on the very first miss.
const grabVanishedGrace = 10 * time.Minute

// PollOnce checks every "grabbed" (in-flight) grab against its download
// client's current queue and acts on whichever ones have moved on: a
// completed/seeded item gets imported (see importGrab), a failed one is
// recorded as failed, and one that's vanished from its client's queue
// entirely — without that client having simply failed to answer this pass,
// and without the grab being fresh enough that the client just hasn't
// caught up yet (see grabVanishedGrace) — is treated the same way (removed
// directly in the client, or lost to a client restart).
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult

	grabs, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		s.logger.Error("importer: list grabbed downloads", "error", err)
		return result
	}
	if len(grabs) == 0 {
		return result
	}

	items, _, err := s.downloads.Queue(ctx)
	if err != nil {
		s.logger.Error("importer: queue downloads", "error", err)
		return result
	}
	failedClients := s.downloads.FailedClients()

	byKey := make(map[string]download.Item, len(items))
	for _, it := range items {
		byKey[queueKey(it.ConfigID, it.ID)] = it
	}

	for _, g := range grabs {
		result.Checked++
		item, ok := byKey[queueKey(g.ClientConfigID, g.ClientItemID)]
		if !ok {
			if failedClients[g.ClientConfigID] {
				continue // that client just didn't answer this pass — not a true orphan
			}
			if grabbedAt, err := time.Parse(time.DateTime, g.GrabbedAt); err == nil && time.Since(grabbedAt) < grabVanishedGrace {
				continue // too soon to conclude it's gone — see grabVanishedGrace
			}
			s.failGrab(g, "no longer in the download client's queue (removed there, or lost to a client restart)")
			result.Failed++
			continue
		}
		switch item.Status {
		case "completed", "seeded":
			if s.importGrab(ctx, g, item) {
				result.Imported++
			} else {
				result.Failed++
			}
		case "failed":
			s.failGrab(g, "download client reported it failed")
			result.Failed++
		}
	}
	return result
}

// queueKey lowercases itemID: a magnet's info hash is stored lowercase (see
// download.magnetHash), but not every qBittorrent-compatible bridge reports
// it back that way — a debrid bridge in particular routinely echoes the
// hash in whatever case the original magnet URI used. Without normalizing
// here, a grab and its own queue item can silently stop matching, which
// PollOnce reads as "vanished from the queue" and fails a perfectly healthy
// torrent. The qBittorrent client already treats hash comparison this way
// internally (see qbittorrent.go's own use of strings.EqualFold); this
// applies the same rule where grabs are matched back to the live queue.
func queueKey(configID int64, itemID string) string {
	return fmt.Sprintf("%d:%s", configID, strings.ToLower(itemID))
}

// failGrab records a grab as failed and, if it was made for a wanted
// album, reverts that album back to "wanted" so the user can search again
// and try a different release — the same as if it had never been grabbed,
// rather than leaving it stuck at "downloading" forever.
func (s *Service) failGrab(g download.GrabRecord, message string) {
	if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed, message); err != nil {
		s.logger.Error("importer: resolve failed grab", "grab_id", g.ID, "error", err)
		return
	}
	if g.WantedAlbumID > 0 {
		if err := s.music.SetWantedAlbumStatus(g.WantedAlbumID, musiclibrary.WantedStatusWanted); err != nil {
			s.logger.Error("importer: revert wanted album status", "grab_id", g.ID, "wanted_album_id", g.WantedAlbumID, "error", err)
		}
	}
}

// targetRootFolder picks which music root folder a completed grab's files
// should land in: the artist's own existing root folder when it already
// has one, so an artist's discography never splits across folders purely
// because of where a later grab happened to import to. A brand-new artist
// (no existing files anywhere) falls back to whichever root folder is
// currently marked default. ok is false only when no music root folder is
// configured at all.
func (s *Service) targetRootFolder(g download.GrabRecord) (rf musiclibrary.RootFolder, ok bool) {
	var artistID int64
	switch {
	case g.WantedAlbumID > 0:
		if w, err := s.music.GetWantedAlbum(g.WantedAlbumID); err == nil {
			artistID = w.ArtistID
		}
	case g.UpgradeAlbumID > 0:
		if a, err := s.music.GetAlbum(g.UpgradeAlbumID); err == nil {
			artistID = a.ArtistID
		}
	}
	if artistID > 0 {
		if existing, err := s.music.ArtistRootFolder(artistID); err == nil {
			return *existing, true
		}
	}
	if def, err := s.music.DefaultRootFolder(); err == nil {
		return *def, true
	}
	// No root folder is marked default — shouldn't happen once any music
	// root folder exists (the API always marks the first one added as
	// default), but falling back to whichever sorts first rather than
	// failing the import outright means a stray data inconsistency here
	// can't silently strand every future grab with nowhere to go.
	folders, err := s.music.ListRootFolders()
	if err != nil || len(folders) == 0 {
		return musiclibrary.RootFolder{}, false
	}
	return folders[0], true
}

// expectedReleaseGroupMBID returns the release group g's own
// wanted_albums/albums row already names, if any — the same known target
// targetRootFolder resolves an artist from, one field further. Empty when
// neither ID is set (shouldn't happen for a real grab) or the row it
// pointed at is already gone; importGrab treats that exactly like a
// manually-added file, falling through to ordinary tag-based matching.
func (s *Service) expectedReleaseGroupMBID(g download.GrabRecord) string {
	switch {
	case g.WantedAlbumID > 0:
		if w, err := s.music.GetWantedAlbum(g.WantedAlbumID); err == nil {
			return w.ReleaseGroupMBID
		}
	case g.UpgradeAlbumID > 0:
		if a, err := s.music.GetAlbum(g.UpgradeAlbumID); err == nil {
			return a.ReleaseGroupMBID
		}
	}
	return ""
}

// seedExpectedReleaseGroup stamps releaseGroupMBID onto every one of
// copiedPaths (copyTree's own return value — the exact files it just
// wrote, already known without a second walk of dest) via
// SeedExpectedReleaseGroup, before the scan that actually discovers and
// matches them runs — see that method's own doc comment for why this
// ordering matters. A no-op (not an error) when releaseGroupMBID is
// empty: nothing to stamp for a grab whose own wanted/upgrade target
// couldn't be resolved, same as a manually-added file.
func (s *Service) seedExpectedReleaseGroup(rootFolderID int64, copiedPaths []string, releaseGroupMBID string) {
	if releaseGroupMBID == "" {
		return
	}
	for _, path := range copiedPaths {
		if err := s.music.SeedExpectedReleaseGroup(rootFolderID, path, releaseGroupMBID); err != nil {
			s.logger.Warn("importer: seed expected release group", "path", path, "error", err)
		}
	}
}

// stillGrabbed reports whether id is still in GrabStatusGrabbed right now
// — a fresh DB read, not the possibly-stale GrabRecord importGrab was
// called with. Fails open (true) on a read error: a transient DB hiccup
// shouldn't abandon an otherwise-healthy import.
func (s *Service) stillGrabbed(id int64) bool {
	g, err := s.downloads.Store().GetGrab(id)
	if err != nil {
		s.logger.Warn("importer: re-checking grab status", "grab_id", id, "error", err)
		return true
	}
	return g.Status == download.GrabStatusGrabbed
}

// importGrab copies a completed download's audio files (see copyTree —
// everything else the download brought along is left behind, not copied
// in) from item.Path (the download client's own local disk, translated
// through the configured remote path mappings — see config.TranslatePath)
// into a root folder chosen by targetRootFolder, then scans that root so
// the copied files are matched/organized the normal way, same as any
// other file dropped there. A copy, not a move, until the copy itself is
// confirmed good — only once it succeeds is the source removed from the
// download client (with its data, junk included), so a failed or partial
// copy never loses the only remaining copy of the download. Reports
// whether the grab ended up imported.
func (s *Service) importGrab(ctx context.Context, g download.GrabRecord, item download.Item) bool {
	if item.Path == "" {
		s.logger.Warn("importer: completed download reported no path, leaving it for manual import",
			"grab_id", g.ID, "title", g.Title)
		return false
	}
	root, ok := s.targetRootFolder(g)
	if !ok {
		s.logger.Error("importer: no music root folder configured, leaving grab for manual import", "grab_id", g.ID)
		return false
	}

	// Re-check this grab's live status right before doing anything slow: a
	// concurrent artist/album removal (internal/api's cancelInFlightGrabs)
	// can mark it failed between PollOnce's own listing and this call
	// actually running. Catching that here — before the copy, and again
	// before the scan below — means the removal doesn't get silently
	// undone by an import that started before it landed but hadn't
	// finished yet. Not a full close of the race (the copy/scan themselves
	// aren't atomic with these checks), but it collapses the window from
	// "however long a copy+scan takes" down to "however long each check
	// takes," which is what actually matters in practice.
	if !s.stillGrabbed(g.ID) {
		s.logger.Info("importer: grab was resolved elsewhere before import started, skipping",
			"grab_id", g.ID)
		return false
	}

	src := config.TranslatePath(s.cfg.PathMappings(), item.Path)
	dest := filepath.Join(root.Path, filepath.Base(src))
	copiedPaths, err := copyTree(src, dest)
	if err != nil {
		s.logger.Error("importer: copy failed", "grab_id", g.ID, "src", src, "dest", dest, "error", err)
		s.failGrab(g, fmt.Sprintf("copy from download client failed: %v", err))
		return false
	}
	if len(copiedPaths) == 0 {
		// copyTree found nothing worth copying — a genuinely empty/wrong
		// release, not a copy error. Found live: this used to fall through
		// to the success path below regardless, which resolved the grab as
		// imported, deleted its wanted_albums row (so the album could never
		// be automatically retried), and deleted the "completed" download's
		// data — for content that was never actually added to the library.
		// A real failure, so it's treated as one: failGrab reverts a wanted
		// album back to "wanted" the same as any other failed grab, and the
		// source is left in the download client for the user to inspect
		// rather than silently discarded.
		s.logger.Warn("importer: no audio files found in completed download, nothing imported",
			"grab_id", g.ID, "src", src)
		s.failGrab(g, "completed download contained no recognized audio files")
		return false
	}
	// Stamps the files just copied with the release group this grab was
	// actually for, before the scan below discovers/matches them — lets
	// the folder-level matcher skip straight to the known target instead
	// of re-deriving it blind from tags, the same way a manually-added
	// file still has to. See expectedReleaseGroupMBID/
	// seedExpectedReleaseGroup's own doc comments.
	s.seedExpectedReleaseGroup(root.ID, copiedPaths, s.expectedReleaseGroupMBID(g))

	if !s.stillGrabbed(g.ID) {
		// The files are already safely copied to dest — left in place
		// rather than deleted, so nothing already-copied is lost — but
		// scanning them in now would recreate whatever the concurrent
		// removal this grab raced against just deleted, straight from
		// their own embedded tags. Leave them for a manual scan/review
		// instead of resurrecting anything automatically.
		s.logger.Info("importer: grab was resolved elsewhere mid-copy, leaving the copied files unscanned",
			"grab_id", g.ID, "dest", dest)
		return false
	}
	// Captured before the scan (which is what actually matches the new
	// files in) so swapUpgradedFiles below can tell which files are the
	// ones this upgrade is meant to replace, as opposed to whatever the
	// scan itself just added.
	var beforeUpgrade []musiclibrary.TrackFile
	if g.UpgradeAlbumID > 0 {
		var err error
		beforeUpgrade, err = s.music.ListTrackFilesByAlbum(g.UpgradeAlbumID)
		if err != nil {
			s.logger.Warn("importer: list track files before upgrade import, old file(s) will be left in place",
				"grab_id", g.ID, "upgrade_album_id", g.UpgradeAlbumID, "error", err)
		}
	}
	if _, err := s.scanner.ScanAll(ctx); err != nil {
		s.logger.Warn("importer: post-import scan failed, the next scan will still pick these files up",
			"grab_id", g.ID, "error", err)
	}
	if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusImported, ""); err != nil {
		s.logger.Error("importer: resolve imported grab", "grab_id", g.ID, "error", err)
		return false
	}
	if g.WantedAlbumID > 0 {
		// The album is owned now — a real albums row exists for it (just
		// created by the scan above). Delete the wanted_albums row rather
		// than marking it "downloaded": a status that lingers would clutter
		// the Wanted card with something no longer actionable forever, and
		// ListMissingArtistReleaseGroups already excludes anything with a
		// real albums row, so nothing needs the wanted row to stay around
		// to keep it out of Missing either.
		if err := s.music.DeleteWantedAlbum(g.WantedAlbumID); err != nil && !errors.Is(err, musiclibrary.ErrNotFound) {
			s.logger.Error("importer: remove satisfied wanted album", "grab_id", g.ID, "wanted_album_id", g.WantedAlbumID, "error", err)
		}
	}
	if g.UpgradeAlbumID > 0 {
		s.swapUpgradedFiles(g.UpgradeAlbumID, beforeUpgrade)
	}
	// The copy is safely in the library now — remove the download from its
	// client, deleting its own data too, so a finished grab doesn't sit
	// around in the download folder or the client's history forever.
	// Best-effort: the import already succeeded either way.
	if err := s.downloads.Remove(ctx, g.ClientConfigID, g.ClientItemID, true); err != nil {
		s.logger.Warn("importer: removing completed download from its client failed (import still succeeded)",
			"grab_id", g.ID, "error", err)
	}
	// Some clients (debrid bridges in particular) acknowledge the removal
	// but don't actually honor the delete-files flag — CantiNode already
	// has its own safely-copied version in the library, so delete the
	// source directly to be sure it's actually gone instead of trusting
	// the client did what it was asked.
	deleteDownloadData(src, s.logger)

	s.logger.Info("importer: imported completed download", "grab_id", g.ID, "title", g.Title, "dest", dest)
	return true
}

// swapUpgradedFiles deletes the old, now-superseded file for each track an
// upgrade grab (handleGrabAlbumUpgrade, tracked via GrabRecord.UpgradeAlbumID)
// just replaced with a better one. before is the album's track files
// captured right before the scan that imports and matches the new ones in
// — comparing against the album's current state after that scan identifies
// exactly which files are new. Deliberately swaps track-by-track rather
// than wiping every pre-existing file once anything new shows up: a track
// the new release didn't end up matching (a partial or failed match) keeps
// whatever file it already had, so a bad upgrade can never leave a track
// with nothing. Best-effort and non-fatal — the import itself already
// succeeded either way, so a failure here is logged and left for manual
// cleanup, never treated as reason to fail the import.
func (s *Service) swapUpgradedFiles(albumID int64, before []musiclibrary.TrackFile) {
	beforeIDs := make(map[int64]bool, len(before))
	oldByTrack := make(map[int64][]musiclibrary.TrackFile)
	for _, tf := range before {
		beforeIDs[tf.ID] = true
		if tf.TrackID != nil {
			oldByTrack[*tf.TrackID] = append(oldByTrack[*tf.TrackID], tf)
		}
	}

	after, err := s.music.ListTrackFilesByAlbum(albumID)
	if err != nil {
		s.logger.Warn("importer: list track files after upgrade import, old file(s) left in place",
			"album_id", albumID, "error", err)
		return
	}

	newlyMatchedTracks := make(map[int64]bool)
	for _, tf := range after {
		if beforeIDs[tf.ID] || tf.TrackID == nil || tf.MatchStatus != musiclibrary.StatusMatched {
			continue
		}
		newlyMatchedTracks[*tf.TrackID] = true
	}

	for trackID := range newlyMatchedTracks {
		for _, old := range oldByTrack[trackID] {
			if err := os.Remove(old.Path); err != nil && !os.IsNotExist(err) {
				s.logger.Warn("importer: deleting file superseded by an upgrade failed, leaving its row in place",
					"album_id", albumID, "path", old.Path, "error", err)
				continue
			}
			if err := s.music.DeleteTrackFile(old.ID); err != nil {
				s.logger.Warn("importer: deleting superseded track file row failed",
					"album_id", albumID, "track_file_id", old.ID, "error", err)
				continue
			}
			s.logger.Info("importer: deleted file superseded by an upgrade", "album_id", albumID, "path", old.Path)
		}
	}
}

// deleteDownloadData removes a completed download's own files after a
// successful import, guarding against a misreported path: it must be
// absolute and nested at least three segments deep (e.g.
// .../downloads/<client>/<release>) so a bad or mistranslated path can
// never wipe a mount root or top-level directory.
func deleteDownloadData(path string, logger *slog.Logger) {
	if path == "" || !filepath.IsAbs(path) {
		return
	}
	clean := filepath.Clean(path)
	segs := strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == '\\' })
	if len(segs) < 3 {
		logger.Warn("importer: refusing to delete a suspiciously shallow download path", "path", clean)
		return
	}
	if err := os.RemoveAll(clean); err != nil {
		logger.Warn("importer: deleting download files failed", "path", clean, "error", err)
	}
}

// copyTree copies only the audio files under src (a file or a directory,
// recursively) into dstDir, preserving relative structure — everything
// else a download brings along (NFOs, cover-art images, .cue/.m3u/.sfv
// sidecar files, sample clips, "Proof" folders...) is deliberately left
// behind rather than cluttering the library, and (via deleteDownloadData)
// removed along with the rest of the source once import succeeds. A
// non-audio src passed directly (shouldn't normally happen — a grab's own
// path is always a directory) is skipped rather than erroring, consistent
// with a directory that turns out to hold no audio at all: copied comes
// back empty, not an error, so the caller decides whether that's worth
// treating as a failure. A subdirectory with no audio files anywhere
// under it is never created at the destination.
//
// Returns every destination path it actually wrote, not just a count —
// this is the one walk of the copied tree the whole import does; a caller
// needing to know which files landed where (seedExpectedReleaseGroup)
// uses this instead of re-walking dstDir itself afterward.
func copyTree(src, dstDir string) (copiedPaths []string, err error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat source %s: %w", src, err)
	}
	if !info.IsDir() {
		if !tagreader.IsAudioFile(src) {
			return nil, nil
		}
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return nil, err
		}
		return []string{dst}, nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("read source dir %s: %w", src, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dstDir, e.Name())
		if e.IsDir() {
			n, err := copyTree(s, d)
			if err != nil {
				return copiedPaths, err
			}
			copiedPaths = append(copiedPaths, n...)
			continue
		}
		if !tagreader.IsAudioFile(s) {
			continue
		}
		if err := copyFile(s, d); err != nil {
			return copiedPaths, err
		}
		copiedPaths = append(copiedPaths, d)
	}
	return copiedPaths, nil
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

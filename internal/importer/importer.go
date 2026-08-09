// Package importer is Completed Download Handling: it watches download
// clients for finished grabs, copies the book file into the library laid out
// by the naming templates, records it, and resolves the grab. Files are
// copied (never moved) so torrents keep seeding; usenet history entries are
// cleaned up after import.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/librinode/librinode/internal/comicinfo"
	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/opf"
	"github.com/librinode/librinode/internal/organize"
	"github.com/librinode/librinode/internal/release"
	"github.com/librinode/librinode/internal/scanner"
)

type Service struct {
	store     *library.Store
	downloads *download.Service
	organize  *organize.Service
	// settings (optional) reports the current Completed Download Handling
	// options — pack-import-all, and the post-import client/file cleanup.
	settings func() config.ImportSettings
	// research (optional) starts a fresh search for a book whose download was
	// blocklisted as junk, so acquisition moves straight to another release.
	research func(bookID int64, mediaType string)
	// pathMappings (optional) reports the remote→local path mappings applied
	// to client-reported download paths before any disk access.
	pathMappings func() []config.PathMapping
	// runMu serializes Run passes: the periodic sweep (every ImportInterval)
	// and a manual "Import now" click can otherwise overlap, and a pack
	// import's cleanup step (RemoveAll-ing the whole download folder) racing
	// against a second, still-in-progress pass copying that same download's
	// other book out of it would corrupt or silently drop that book's file.
	runMu sync.Mutex
}

func New(store *library.Store, downloads *download.Service, org *organize.Service, settings func() config.ImportSettings) *Service {
	return &Service{store: store, downloads: downloads, organize: org, settings: settings}
}

// OnJunkBlocklist registers a callback the importer fires (in the background)
// after it blocklists a bad/spam download, so a replacement is searched for
// immediately instead of waiting for the next periodic sweep. Optional.
func (s *Service) OnJunkBlocklist(fn func(bookID int64, mediaType string)) {
	s.research = fn
}

// SetPathMappings registers the remote→local path mapping provider
// (Settings → Download Clients → Remote path mappings). Optional.
func (s *Service) SetPathMappings(fn func() []config.PathMapping) {
	s.pathMappings = fn
}

// opts returns the current import settings, tolerating a nil provider.
func (s *Service) opts() config.ImportSettings {
	if s.settings == nil {
		return config.ImportSettings{}
	}
	return s.settings()
}

// errDownloadPending marks a download the client reports as done but whose
// files aren't readable yet — the path is missing because it's still syncing
// (a network share, or a debrid bridge that finalizes after reporting
// complete). The import is retried on the next pass instead of being failed and
// the release blocklisted.
var errDownloadPending = errors.New("download not ready")

// Result summarizes one import pass.
type Result struct {
	Imported int      `json:"imported"`
	Failed   int      `json:"failed"`
	Skipped  int      `json:"skipped"`
	Messages []string `json:"messages,omitempty"`
}

func (r *Result) note(format string, args ...any) {
	r.Messages = append(r.Messages, fmt.Sprintf(format, args...))
}

// Run performs one import pass over all download clients. Serialized against
// any other in-progress pass (the periodic sweep and a manual "Import now"
// click both call this) — see runMu.
func (s *Service) Run(ctx context.Context) (*Result, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	result := &Result{Messages: []string{}}

	items, clientErrs, err := s.downloads.Queue(ctx)
	if err != nil {
		return nil, err
	}
	result.Messages = append(result.Messages, clientErrs...)

	// Clients on other machines/containers report their own filesystem;
	// remote path mappings translate every reported path onto ours before
	// anything touches disk.
	if s.pathMappings != nil {
		if mappings := s.pathMappings(); len(mappings) > 0 {
			for i := range items {
				items[i].Path = config.TranslatePath(mappings, items[i].Path)
			}
		}
	}

	pending, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		return nil, err
	}
	imported, err := s.downloads.Store().ListGrabs(download.GrabStatusImported)
	if err != nil {
		return nil, err
	}

	matched := map[int64]bool{}
	for i := range items {
		item := &items[i]
		grab := matchGrab(pending, item)
		if grab != nil {
			matched[grab.ID] = true
		}
		switch item.Status {
		case "completed":
			s.importItem(ctx, item, grab, result)
		case "seeded":
			// Seed goal reached (the client paused/stopped the finished
			// torrent). Import it if we never did, then clean up: an
			// already-imported grab's torrent is removed with its data.
			if grab != nil {
				s.importItem(ctx, item, grab, result)
				continue
			}
			if g := matchGrab(imported, item); g != nil {
				if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, true); err != nil {
					result.note("removing seeded %s: %v", item.Title, err)
				} else {
					slog.Info("removed torrent after seeding goal", "title", item.Title)
					result.note("removed %s after seeding goal", item.Title)
				}
			}
		case "failed":
			if grab == nil {
				continue
			}
			_ = s.downloads.Store().ResolveGrab(grab.ID, download.GrabStatusFailed, "download failed in client")
			// Never grab this release again; search falls to the next candidate.
			if err := s.downloads.Store().AddBlock(grab.GUID, grab.Title, "download failed in client"); err != nil {
				result.note("blocklisting %s: %v", grab.Title, err)
			}
			// Failed downloads are junk in the client; clean up data too. Some
			// clients (debrid bridges) ignore the delete-files flag, so also
			// delete the download's folder directly.
			if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, true); err != nil {
				result.note("removing failed %s: %v", item.Title, err)
			}
			deleteDownloadData(item.Path, result)
			result.Failed++
		}
	}

	// Orphan sweep: a pending grab whose download no longer appears in its
	// client (removed via the client's own UI, purged by a bridge) would stay
	// "grabbed" forever. Past the grace period it's resolved as failed — no
	// blocklist, the release itself may be fine — and re-searched. Exempted
	// per client, not globally: a grab is only skipped when ITS client failed
	// to answer this pass, so one down torrent client can't freeze orphan
	// resolution for grabs sitting in a perfectly healthy usenet client.
	failedClients := s.downloads.FailedClients()
	for i := range pending {
		g := &pending[i]
		if matched[g.ID] || grabAge(g) < stalePendingGrace || failedClients[g.ClientConfigID] {
			continue
		}
		_ = s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed,
			"download disappeared from the download client")
		result.note("%s: disappeared from the download client", g.Title)
		if s.research != nil && g.BookID > 0 {
			go s.research(g.BookID, g.MediaType)
		}
		result.Failed++
	}

	if result.Imported > 0 || result.Failed > 0 {
		slog.Info("import pass complete",
			"imported", result.Imported, "failed", result.Failed, "skipped", result.Skipped)
	}
	return result, nil
}

// writeOPF drops the metadata sidecar next to an imported book: <file>.opf
// beside the flat ebook file.
func (s *Service) writeOPF(book *library.Book, mediaType, target, dir string) error {
	author, err := s.store.GetAuthor(book.AuthorID)
	if err != nil {
		return err
	}
	series, err := s.store.ListSeriesForBook(book.ID)
	if err != nil {
		return err
	}
	full, err := s.store.GetBook(book.ID) // detail includes editions (ISBN, language)
	if err != nil {
		full = book
	}
	data, err := opf.Render(full, author.Name, series)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "metadata.opf")
	if mediaType == "ebook" {
		path = strings.TrimSuffix(target, filepath.Ext(target)) + ".opf"
	}
	return os.WriteFile(path, data, 0o644)
}

// matchGrab pairs a queue item with its grab record: by the client's item id
// first, by normalized title otherwise. The title fallback isn't restricted
// to grabs with no client item id — a torrent grab's id can still be wrong
// (a same-titled item already in the client at grab time, or a record from
// before the client-item-id fix ever existed) — so it stays a safety net
// even for a grab that does carry one, rather than leaving that grab
// unmatchable forever.
func matchGrab(pending []download.GrabRecord, item *download.Item) *download.GrabRecord {
	for i := range pending {
		g := &pending[i]
		if g.ClientItemID != "" && g.ClientItemID == item.ID {
			return g
		}
	}
	itemTitle := scanner.Normalize(item.Title)
	for i := range pending {
		g := &pending[i]
		if scanner.Normalize(g.Title) == itemTitle {
			return g
		}
	}
	return nil
}

func (s *Service) importItem(ctx context.Context, item *download.Item, grab *download.GrabRecord, result *Result) {
	// Which book is this? Grab record first, title parse as fallback for
	// downloads added outside LibriNode's grab flow (ebook-only fallback:
	// comic imports always come from tracked grabs).
	mediaType := "ebook"
	var book *library.Book
	var err error
	if grab != nil && grab.BookID > 0 {
		if grab.MediaType != "" {
			mediaType = grab.MediaType
		}
		book, err = s.store.GetBook(grab.BookID)
		if err != nil {
			s.resolve(grab, download.GrabStatusFailed, "book no longer in library")
			result.Failed++
			return
		}
	} else {
		book = s.matchByTitle(item.Title)
	}
	if book == nil {
		result.Skipped++
		return // not ours to import (yet); stays in the client
	}
	owned := book.HasEbookFile
	// Untracked downloads never replace existing files; tracked grabs for
	// owned books may be upgrades — decided below once the format is known.
	if owned && grab == nil {
		result.Skipped++
		return
	}

	var sources []string
	var format string
	var pack *packPlan // set when an ebook/comic download is a multi-book release
	switch mediaType {
	case "comic":
		var source string
		source, pack, err = s.pickPackAware(item.Path, scanner.IsComicPath, "comic archive", grab, book, mediaType)
		sources = []string{source}
		format = fileFormat(source)
	default:
		var source string
		source, pack, err = s.pickPackAware(item.Path, scanner.IsEbookPath, "ebook", grab, book, mediaType)
		sources = []string{source}
		format = fileFormat(source)
	}
	if err != nil {
		switch {
		case grab == nil:
			result.Skipped++ // untracked download, not ours to fail
		case !errors.Is(err, errDownloadPending):
			// The download finished but its content is wrong: mislabeled, or —
			// common on usenet — spam/malware (an .exe in place of the book).
			reason := err.Error()
			if junk := junkFile(item.Path); junk != "" {
				reason = fmt.Sprintf("spam release — contains %q, not a %s file", junk, mediaType)
			}
			s.abandon(ctx, item, grab, mediaType, reason, result)
		case unresolvablePath(item.Path, grab):
			// The client reports the download done but its files never became
			// readable, while the download area itself IS reachable — a path
			// that will never resolve (special-character names land in short
			// 8.3-style folders the client reports under a mangled path). Give
			// up instead of retrying forever. A transient share/mount outage
			// (parent unreachable) is excluded, so good releases survive it.
			s.abandon(ctx, item, grab, mediaType,
				"download completed but its files never became readable (unresolvable path)", result)
		case grabAge(grab) >= stalePendingGrace:
			// Past the grace period and still no usable file, even though the
			// download path itself is reachable (a debrid mount that shows the
			// folder before its contents finish syncing can report zero
			// matching files for a while — see errDownloadPending below). Give
			// up rather than retry forever.
			s.abandon(ctx, item, grab, mediaType,
				"download completed but never yielded a usable file after a long wait", result)
		default:
			// Files not ready yet (still syncing) or a momentary share hiccup —
			// leave the grab pending and retry next pass. Noted (not just
			// counted) so a manual "Import now" surfaces why, instead of a
			// bare skip count with no way to tell a sync delay from anything
			// else — the periodic sweep's own summary log line never carried
			// this detail at all.
			result.note("%s: %v", item.Title, err)
			result.Skipped++
		}
		return
	}

	// Owned + tracked grab: only proceed when the new format genuinely
	// upgrades the owned one; the old files are replaced after import. Even
	// when the grabbed book itself isn't an upgrade, a pack's OTHER books
	// still need placing — skipping the primary must not skip the pack.
	var replacing []library.BookFile
	skipPrimary := false
	if owned {
		old, better := s.upgradeCheck(book, mediaType, format)
		if !better {
			s.resolve(grab, download.GrabStatusImported,
				"book already has a "+mediaType+" file (not an upgrade)")
			result.Skipped++
			skipPrimary = true
		} else {
			replacing = old
		}
	}

	if !skipPrimary {
		target, ok := s.placeAndRecord(book, mediaType, format, sources, replacing, item.Title, result)
		if !ok {
			return
		}

		if grab != nil {
			message := "imported to " + target
			if len(replacing) > 0 {
				message = "upgraded (" + replacing[0].Format + " → " + format + "), imported to " + target
			}
			s.resolve(grab, download.GrabStatusImported, message)
		}
		result.Imported++
		slog.Info("imported download", "book", book.Title, "path", target)
	}

	// Multi-book pack: the download's other files fill more books, regardless
	// of whether the grabbed book itself needed (or got) a new file. This
	// reads from the download folder, so it must run before any cleanup
	// deletes it.
	if grab != nil && pack != nil {
		s.importPackExtras(pack, sources[0], book, mediaType, result)
	}
	if grab != nil {
		s.cleanupAfterImport(ctx, item, grab, result)
	}
}

// cleanupAfterImport removes an imported download from its client per the
// Completed Download Handling settings. With both options off (the default),
// usenet history entries are cleared — the file stays, LibriNode only copied
// it — and torrents keep seeding. RemoveCompleted removes the download from the
// client for both protocols; DeleteCompletedFiles additionally deletes the
// downloaded files from disk.
func (s *Service) cleanupAfterImport(ctx context.Context, item *download.Item, grab *download.GrabRecord, result *Result) {
	// LibriNode's own direct fetcher streams a flat file into the download folder
	// solely so it can be imported — there is no seeding and no reason to keep the
	// source once it's in the library. Always remove the imported download
	// (deleting the streamed file), regardless of the Completed Download Handling
	// toggles, which exist for external clients that may keep seeding. The file is
	// flat in the download root, so there are no leftover folders to prune.
	if grab.Protocol == download.ProtocolDirect {
		if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, true); err != nil {
			result.note("removing imported %s from direct client: %v", item.Title, err)
		}
		deleteDownloadData(item.Path, result)
		return
	}

	opts := s.opts()
	if opts.RemoveCompleted || opts.DeleteCompletedFiles {
		if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, opts.DeleteCompletedFiles); err != nil {
			result.note("removing %s from client: %v", item.Title, err)
		}
		// Some clients (debrid bridges) acknowledge the removal but ignore the
		// delete-files flag. LibriNode imported from this path, so delete it
		// directly to be sure the source is gone.
		if opts.DeleteCompletedFiles {
			deleteDownloadData(item.Path, result)
		}
		return
	}
	// Default: clear the usenet history entry (no data deleted); leave torrents
	// seeding until the client's own goal is reached.
	if grab.Protocol == download.ProtocolUsenet {
		if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, false); err != nil {
			result.note("cleaning up %s: %v", item.Title, err)
		}
	}
}

// deleteDownloadData removes the download's own files after import, for the
// DeleteCompletedFiles option, guarding against a misreported path: it must be
// absolute and nested at least three segments deep (…/downloads/<client>/
// <release>) so a bad path can never wipe a mount root or top-level directory.
func deleteDownloadData(path string, result *Result) {
	if path == "" || !filepath.IsAbs(path) {
		return
	}
	clean := filepath.Clean(path)
	segs := strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == '\\' })
	if len(segs) < 3 {
		result.note("refusing to delete shallow download path %q", clean)
		return
	}
	if err := os.RemoveAll(clean); err != nil {
		result.note("deleting download files %s: %v", clean, err)
	}
}

// placeAndRecord copies the source files into the library at the naming
// template's path, writes the format's sidecars, records the book file, and
// removes any replaced (upgraded) files. Returns the target path; false means
// the import was skipped and noted in result.
func (s *Service) placeAndRecord(book *library.Book, mediaType, format string, sources []string, replacing []library.BookFile, itemTitle string, result *Result) (string, bool) {
	place, err := s.organize.PlaceFile(book, format, mediaType)
	if err != nil {
		result.note("%s: %v", itemTitle, err)
		result.Skipped++
		return "", false
	}

	var target string
	var size int64
	adopted := false // target already on disk but unrecorded — record, don't copy
	target = filepath.Join(place.Dir, place.FileName)
	if info, err := os.Stat(target); err == nil {
		if len(replacing) > 0 {
			// Upgrade in flight and the target name is taken: never
			// overwrite — leave the owned file alone.
			result.note("%s: target already exists: %s", itemTitle, target)
			result.Skipped++
			return "", false
		}
		// On disk but unrecorded (a previous import or manual placement left
		// it here without a library record). Adopt it instead of skipping
		// forever ("target already exists" every pass).
		size = info.Size()
		adopted = true
		result.note("%s: adopted existing file: %s", itemTitle, target)
	}
	if !adopted {
		if size, err = copyFile(sources[0], target); err != nil {
			result.note("%s: %v", itemTitle, err)
			result.Skipped++
			return "", false
		}
	}

	// Comic archives get a ComicInfo.xml sidecar inside the CBZ so Kavita/
	// Komga pick up series metadata; failures aren't fatal to the import.
	if mediaType == "comic" && format == "cbz" {
		if err := s.writeComicInfo(target, book); err != nil {
			result.note("%s: writing ComicInfo.xml: %v", itemTitle, err)
		}
	}

	// Ebooks get an OPF sidecar (Calibre); failures aren't fatal to the
	// import.
	if mediaType == "ebook" {
		if err := s.writeOPF(book, mediaType, target, place.Dir); err != nil {
			result.note("%s: writing OPF sidecar: %v", itemTitle, err)
		}
	}

	file := &library.BookFile{
		RootFolderID: place.RootFolderID,
		BookID:       book.ID,
		MediaType:    mediaType,
		Path:         target,
		Size:         size,
		Format:       format,
		ModifiedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.store.UpsertBookFile(file); err != nil {
		result.note("%s: recording file: %v", itemTitle, err)
		result.Skipped++
		return "", false
	}

	// Upgrade: the replaced files leave disk and library together.
	keep := []string{target, filepath.Join(place.Dir, "metadata.opf")}
	for _, old := range replacing {
		if strings.EqualFold(old.Path, target) {
			continue
		}
		if dir, err := isDir(old.Path); err == nil && dir {
			if err := removeExcept(old.Path, keep); err != nil {
				result.note("removing upgraded file %s: %v", old.Path, err)
			}
		} else if err := os.RemoveAll(old.Path); err != nil {
			result.note("removing upgraded file %s: %v", old.Path, err)
		}
		if err := s.store.DeleteBookFile(old.ID); err != nil && !errorsIsNotFound(err) {
			result.note("forgetting upgraded file %s: %v", old.Path, err)
		}
	}
	return target, true
}

// isDir reports whether path exists and is a directory.
func isDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// removeExcept deletes every direct entry under dir except the paths in keep
// (matched exactly, or as an ancestor of one), removing dir itself too if
// nothing in keep actually lived there. Used to clean up an old multi-file
// placement's folder without touching a just-placed replacement inside it.
func removeExcept(dir string, keep []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	remaining := 0
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		kept := false
		for _, k := range keep {
			if p == k || strings.HasPrefix(k, p+string(filepath.Separator)) {
				kept = true
				break
			}
		}
		if kept {
			remaining++
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	if remaining == 0 {
		return os.Remove(dir)
	}
	return nil
}

// importPackExtras imports the remaining files of a multi-book release
// ("complete series" bundles). Default policy: only files matching a
// *monitored* library book are imported — grabbing one volume from a pack
// never auto-imports unmonitored ones. The opt-in pack-import-all setting
// lifts that to every matching book (imported ebooks then join their format
// library, like scanned files do). Either way, a book that
// already has this format's file is only replaced when the pack's copy is a
// genuine quality upgrade.
func (s *Service) importPackExtras(pack *packPlan, primary string, grabbed *library.Book, mediaType string, result *Result) {
	importAll := s.opts().PackImportAll
	done := map[int64]bool{grabbed.ID: true}
	for _, f := range pack.files {
		if f == primary {
			continue
		}
		b := pack.matcher.match(f)
		if b == nil || done[b.ID] {
			continue
		}
		done[b.ID] = true
		if !importAll && !monitoredFor(b, mediaType) {
			continue
		}
		format := fileFormat(f)
		var replacing []library.BookFile
		if len(s.ownedFiles(b.ID, mediaType)) > 0 {
			old, better := s.upgradeCheck(b, mediaType, format)
			if !better {
				continue
			}
			replacing = old
		}
		target, ok := s.placeAndRecord(b, mediaType, format, []string{f}, replacing, filepath.Base(f), result)
		if !ok {
			continue
		}
		// Owning a file puts a prose book in the format's library (same as
		// scan); volumes already belong to their series.
		if err := s.store.EnsureBookLibrary(b.ID, mediaType); err != nil {
			result.note("pack: enrolling %s: %v", b.Title, err)
		}
		result.Imported++
		result.note("pack: imported %s for %s", filepath.Base(f), b.Title)
		slog.Info("imported pack extra", "book", b.Title, "path", target)
	}
}

// packMatcher resolves a pack's files to library books from data fetched
// once per download: the grabbed volume's series (comic) or the grabbed
// book's author's bibliography (ebooks).
type packMatcher struct {
	mediaType string
	volumes   []library.Book    // comic: the series' volumes…
	positions map[int64]float64 // …and their volume numbers
	books     []library.Book    // ebook: the author's books
}

func (s *Service) newPackMatcher(grabbed *library.Book, mediaType string) *packMatcher {
	m := &packMatcher{mediaType: mediaType}
	switch mediaType {
	case "comic":
		links, err := s.store.ListSeriesForBook(grabbed.ID)
		if err != nil || len(links) == 0 {
			return m
		}
		if m.positions, err = s.store.SeriesBookPositions(links[0].SeriesID); err != nil {
			return m
		}
		m.volumes, _ = s.store.ListVolumes(links[0].SeriesID)
	default: // ebook
		m.books, _ = s.store.ListBooks(grabbed.AuthorID)
	}
	return m
}

// match resolves one file to a library book: comic files match by volume
// number within the series; ebooks match by title, and only when the match
// is unambiguous.
func (m *packMatcher) match(path string) *library.Book {
	switch m.mediaType {
	case "comic":
		number := scanner.VolumeFromName(filepath.Base(path))
		if number == 0 {
			return nil
		}
		for i := range m.volumes {
			if m.positions[m.volumes[i].ID] == number {
				return &m.volumes[i]
			}
		}
		return nil
	default: // ebook
		norm := scanner.Normalize(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		matches := bestBookMatches(norm, m.books)
		if len(matches) != 1 {
			return nil // no match, or two distinct books genuinely tie
		}
		return matches[0]
	}
}

// bestBookMatches finds every prose book whose title appears in norm, then
// discards two kinds of false "second book" evidence:
//
//   - A shorter matched key that's a substring of another matched book's
//     longer key ("Dune" inside "Dune Messiah") — spillover from the more
//     specific title being present, not an independent second book.
//   - A tie on the exact same matched text where the winning book only
//     reaches it through a fallback key (TitleKeys' parenthetical-stripped
//     variant), while another book reaches the identical text through its
//     own primary, undecorated title. A messy bibliography's duplicate or
//     split-edition rows ("Dune Messiah (1 of 2)", "Dune Messiah (2 of 2)")
//     strip down to exactly the real book's title; without this, they'd
//     tie with it on every one of its own releases and the file would
//     never resolve to just the one real book.
//
// What's left are the titles norm genuinely, distinctly names; two or more
// remaining (rather than one merging into the other) is a real tie.
func bestBookMatches(norm string, books []library.Book) []*library.Book {
	type candidate struct {
		book *library.Book
		key  string // longest matching key for this book
		// priority is this key's index within TitleKeys — 0 is the book's
		// own primary title, higher indexes are fallback variants reached
		// only by stripping a subtitle or parenthetical suffix.
		priority int
	}
	var candidates []candidate
	for i := range books {
		b := &books[i]
		if b.MediaType != "book" {
			continue
		}
		best := -1
		var bestKey string
		for idx, key := range scanner.TitleKeys(b.Title) {
			if key != "" && strings.Contains(norm, key) && len(key) > len(bestKey) {
				best, bestKey = idx, key
			}
		}
		if best != -1 {
			candidates = append(candidates, candidate{b, bestKey, best})
		}
	}
	var out []*library.Book
	for i, c := range candidates {
		dominated := false
		for j, other := range candidates {
			if i == j {
				continue
			}
			switch {
			case len(other.key) > len(c.key) && strings.Contains(other.key, c.key):
				dominated = true
			case other.key == c.key && other.priority < c.priority:
				dominated = true
			}
			if dominated {
				break
			}
		}
		if !dominated {
			out = append(out, c.book)
		}
	}
	return out
}

// expectedBookCount estimates how many distinct books a release's own title
// promises, by counting how many of the author's book titles it mentions —
// the same signal release.Score uses to flag a release as a pack. Used to
// tell "this genuinely is a single-book download" apart from "this is a pack
// whose folders haven't all appeared on disk yet, so it currently LOOKS like
// a single-book download" — the two are indistinguishable from the
// filesystem alone; the release's own name is the only independent signal
// of how many books should eventually show up. Comic packs don't need this:
// a series' volume count is already known from its own metadata, not
// guessed from a title. Returns 1 (never less) when nothing more than the
// grabbed book itself is named, or the matcher has no bibliography loaded.
func (m *packMatcher) expectedBookCount(releaseTitle string) int {
	if m.mediaType == "comic" {
		return 1
	}
	relNorm := scanner.Normalize(releaseTitle)
	count := len(bestBookMatches(relNorm, m.books))
	if count < 1 {
		return 1
	}
	return count
}

// monitoredFor reports whether the book is monitored for the media type —
// prose books monitor per format library, volumes/issues use the plain flag.
func monitoredFor(b *library.Book, mediaType string) bool {
	if mediaType == "ebook" {
		return b.InEbookLibrary && b.EbookMonitored
	}
	return b.Monitored
}

// ownedFiles returns the book's files of one media type.
func (s *Service) ownedFiles(bookID int64, mediaType string) []library.BookFile {
	files, err := s.store.ListBookFiles(bookID)
	if err != nil {
		return nil
	}
	owned := []library.BookFile{}
	for _, f := range files {
		if f.MediaType == mediaType {
			owned = append(owned, f)
		}
	}
	return owned
}

// upgradeCheck decides whether newFormat genuinely upgrades the book's
// owned files of this media type (per the type's quality profile), returning
// the files to replace.
func (s *Service) upgradeCheck(book *library.Book, mediaType, newFormat string) ([]library.BookFile, bool) {
	prefs := release.PreferencesFor(s.store, mediaType)
	newScore, ok := prefs.FormatScores[newFormat]
	if !ok {
		return nil, false
	}
	files, err := s.store.ListBookFiles(book.ID)
	if err != nil {
		return nil, false
	}
	old := []library.BookFile{}
	ownedBest := 0
	for _, f := range files {
		if f.MediaType != mediaType {
			continue
		}
		old = append(old, f)
		if sc, ok := prefs.FormatScores[f.Format]; ok && sc > ownedBest {
			ownedBest = sc
		}
	}
	if len(old) == 0 {
		return nil, false
	}
	return old, newScore > ownedBest
}

func errorsIsNotFound(err error) bool {
	return errors.Is(err, library.ErrNotFound)
}

func (s *Service) resolve(grab *download.GrabRecord, status, message string) {
	if err := s.downloads.Store().ResolveGrab(grab.ID, status, message); err != nil {
		slog.Warn("resolving grab", "grab", grab.ID, "error", err)
	}
}

// matchByTitle finds the library book an untracked download belongs to; nil
// unless the parsed title matches exactly one monitored, fileless book.
func (s *Service) matchByTitle(title string) *library.Book {
	books, err := s.store.ListBooks(0)
	if err != nil {
		return nil
	}
	norm := scanner.Normalize(title)
	var match *library.Book
	for i := range books {
		b := &books[i]
		// Only prose books: the fallback imports as ebook, and volumes/issues
		// are always acquired through tracked grabs.
		if b.MediaType != "book" || b.HasEbookFile || !b.Monitored {
			continue
		}
		for _, key := range scanner.TitleKeys(b.Title) {
			if key != "" && strings.Contains(norm, key) {
				if match != nil {
					return nil // ambiguous
				}
				match = b
				break
			}
		}
	}
	return match
}

// pickPackAware selects the file to import for the grabbed book and, when the
// download is a multi-book pack, also returns the full candidate list for
// pack-extra imports. Single-candidate downloads behave as always: the
// largest acceptable file wins (releases ship samples and extras). Tracked
// multi-file downloads are packs — the grabbed book's file is identified by
// volume number (comic) or title (ebooks), never by size: the largest file
// of a v01–v12 bundle is rarely the volume that was grabbed.
func (s *Service) pickPackAware(path string, accept func(string) bool, kind string, grab *download.GrabRecord, book *library.Book, mediaType string) (string, *packPlan, error) {
	files, err := listAcceptable(path, accept, kind)
	if err != nil {
		return "", nil, err
	}
	if grab == nil {
		return largestFile(files), nil, nil
	}

	matcher := s.newPackMatcher(book, mediaType)
	var match string
	var matchSize int64
	matchedBooks := map[int64]bool{}
	for _, f := range files {
		b := matcher.match(f.path)
		if b == nil {
			continue
		}
		matchedBooks[b.ID] = true
		if b.ID == book.ID && f.size > matchSize {
			match, matchSize = f.path, f.size
		}
	}

	// The release's own title can promise more books than have appeared as
	// files yet (comic always reports 1 here — its volume count comes
	// from series metadata, never guessed from a title). Only applies once
	// at least one file has been attributed to a book: a lone file that
	// matches nothing by title is a naming mismatch, not evidence of a
	// pack, and must not be held up waiting for siblings that were never
	// promised in a way we can see.
	if len(matchedBooks) > 0 {
		if expected := matcher.expectedBookCount(grab.Title); len(matchedBooks) < expected && grabAge(grab) < stalePendingGrace {
			return "", nil, fmt.Errorf(
				"release names %d of this author's books, only %d have appeared in the download so far: %w",
				expected, len(matchedBooks), errDownloadPending)
		}
	}

	if len(files) < 2 {
		return largestFile(files), nil, nil
	}
	if match == "" {
		// Could be a genuine mismatch (wrong release), but could just as
		// easily be a pack whose files are syncing one at a time and the
		// grabbed book's own file simply isn't there yet — the same
		// sync-delay case errDownloadPending exists for elsewhere. Retryable;
		// a truly permanent mismatch still eventually gives up (see
		// stalePendingGrace in the caller).
		return "", nil, fmt.Errorf("multi-file download has no file matching %q: %w", book.Title, errDownloadPending)
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.path)
	}
	return match, &packPlan{files: paths, matcher: matcher}, nil
}

// packPlan carries a multi-book download's candidate files and their
// matcher from primary-file selection to the pack-extras pass.
type packPlan struct {
	files   []string
	matcher *packMatcher
}

type candidateFile struct {
	path string
	size int64
}

// listAcceptable returns every file at path (a file or directory) the matcher
// accepts, with sizes; an error when there are none.
func listAcceptable(path string, accept func(string) bool, kind string) ([]candidateFile, error) {
	if path == "" {
		return nil, fmt.Errorf("client reported no path yet: %w", errDownloadPending)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("download path missing (%v): %w", err, errDownloadPending)
	}
	if !info.IsDir() {
		if !accept(path) {
			return nil, fmt.Errorf("%s is not a %s", filepath.Base(path), kind)
		}
		return []candidateFile{{path, info.Size()}}, nil
	}
	var files []candidateFile
	anyFile := false
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		anyFile = true
		if !accept(p) {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			files = append(files, candidateFile{p, fi.Size()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		if !anyFile {
			// The download path itself exists and is reachable, but it's
			// completely empty — a debrid mount can show a torrent's folder
			// before its contents finish syncing to the share. Retryable
			// rather than a hard failure: see errDownloadPending.
			return nil, fmt.Errorf("no files have appeared in the download yet: %w", errDownloadPending)
		}
		// The download has files, just none of the kind wanted — spam/wrong
		// content, not a sync delay. Not retryable.
		return nil, fmt.Errorf("no %s found in download", kind)
	}
	return files, nil
}

// stalePendingGrace is how long a completed-but-unreadable download keeps being
// retried before it's abandoned — long enough for a slow share to finish
// syncing, short enough that a permanently unresolvable path clears itself.
const stalePendingGrace = 30 * time.Minute

// abandon fails a grab whose download can't yield the book (wrong content,
// spam, or a path that never became readable), blocklists the release so it is
// never grabbed again, deletes the junk — out of the client and off disk (some
// clients ignore the delete-files flag) — and kicks off a replacement search so
// acquisition moves straight to another release.
func (s *Service) abandon(ctx context.Context, item *download.Item, grab *download.GrabRecord, mediaType, reason string, result *Result) {
	s.resolve(grab, download.GrabStatusFailed, reason)
	if err := s.downloads.Store().AddBlock(grab.GUID, grab.Title, reason); err != nil {
		result.note("blocklisting %s: %v", grab.Title, err)
	}
	if err := s.downloads.Remove(ctx, item.ConfigID, item.ID, true); err != nil {
		result.note("removing junk %s from client: %v", item.Title, err)
	}
	deleteDownloadData(item.Path, result)
	if s.research != nil && grab.BookID > 0 {
		go s.research(grab.BookID, mediaType)
	}
	result.Failed++
}

// unresolvablePath reports that a completed download's path will never become
// readable and should be abandoned: the grab is past the grace period, the path
// isn't there, but the download area (its parent) IS reachable — so it's a
// wrong/mangled path, not a still-syncing file or a share outage.
func unresolvablePath(path string, grab *download.GrabRecord) bool {
	if grabAge(grab) < stalePendingGrace {
		return false
	}
	if path == "" {
		return true // long done, yet the client never reported a usable path
	}
	if _, err := os.Stat(path); err == nil {
		return false // readable after all
	}
	_, err := os.Stat(filepath.Dir(path))
	return err == nil // parent reachable but the download folder is missing
}

// grabAge is how long ago the grab was sent. Unparseable timestamps report as
// fresh (0) so a bad row keeps retrying rather than being abandoned wrongly.
func grabAge(grab *download.GrabRecord) time.Duration {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, grab.GrabbedAt); err == nil {
			return time.Since(t)
		}
	}
	return 0
}

// junkFile returns the name of the first executable/installer found anywhere in
// a completed download — the signature of a spam/malware release masquerading
// as the book (common on usenet). Empty when the download has none.
func junkFile(path string) string {
	if path == "" {
		return ""
	}
	var found string
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if scanner.IsUnwantedFile(p) {
			found = filepath.Base(p)
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// largestFile picks the biggest candidate (callers guarantee at least one).
func largestFile(files []candidateFile) string {
	best := files[0]
	for _, f := range files[1:] {
		if f.size > best.size {
			best = f
		}
	}
	return best.path
}

// fileFormat is the lowercased extension without the dot.
func fileFormat(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// writeComicInfo injects a ComicInfo.xml built from the volume's library
// metadata into an imported CBZ.
func (s *Service) writeComicInfo(cbzPath string, book *library.Book) error {
	info := comicinfo.Info{
		Title:   book.Description, // issue title lives in the description
		Summary: "",
		Writer:  "",
	}
	if author, err := s.store.GetAuthor(book.AuthorID); err == nil {
		info.Writer = author.Name
	}
	if links, err := s.store.ListSeriesForBook(book.ID); err == nil && len(links) > 0 {
		info.Series = links[0].Title
		info.Number = strconv.FormatFloat(links[0].Position, 'f', -1, 64)
	}
	if len(book.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(book.ReleaseDate[:4]); err == nil {
			info.Year = y
		}
	}
	return comicinfo.Inject(cbzPath, info)
}

// copyFile copies (never moves — torrents keep seeding) source into place.
func copyFile(source, target string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	size, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(target)
		return 0, err
	}
	return size, nil
}

// RunPeriodic runs import passes on the interval until ctx is cancelled.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Run(ctx); err != nil {
				slog.Warn("import pass failed", "error", err)
			}
		}
	}
}

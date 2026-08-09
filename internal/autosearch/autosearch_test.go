package autosearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
)

type fx struct {
	svc    *Service
	store  *library.Store
	grabs  *download.Store
	book   *library.Book // wanted
	owned  *library.Book // has a file, must not be searched
	sabAdd []string      // URLs sent to the mock SAB
}

func fixture(t *testing.T, searchXML string) *fx {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)
	f := &fx{store: store}

	// Mock Newznab indexer.
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			w.Write([]byte(`<caps><server title="mock"/></caps>`))
			return
		}
		w.Write([]byte(searchXML))
	}))
	t.Cleanup(idx.Close)

	// Mock SABnzbd tracking addurl calls.
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "addurl":
			f.sabAdd = append(f.sabAdd, r.URL.Query().Get("name"))
			w.Write([]byte(`{"status": true, "nzo_ids": ["nzo_auto"]}`))
		case "queue":
			w.Write([]byte(`{"queue": {"slots": []}}`))
		case "history":
			w.Write([]byte(`{"history": {"slots": []}}`))
		default:
			w.Write([]byte(`{"version": "4.3.2"}`))
		}
	}))
	t.Cleanup(sab.Close)

	indexers := indexer.NewService(indexer.NewStore(db))
	if err := indexers.Store().Add(&indexer.Indexer{
		Name: "mock", Type: indexer.TypeNewznab, BaseURL: idx.URL,
		Categories: "7000,7020", Enabled: true, Priority: 25,
	}); err != nil {
		t.Fatal(err)
	}
	downloads := download.NewService(download.NewStore(db))
	f.grabs = downloads.Store()
	if err := downloads.Store().Add(&download.ClientConfig{
		Name: "sab", Type: download.TypeSABnzbd, Host: sab.URL,
		APIKey: "k", Category: "librinode", Enabled: true, Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}

	author := &library.Author{Source: "hardcover", ForeignID: "100", Name: "Terry Pratchett", Monitored: true}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	f.book = &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "1",
		Title: "Mort", Monitored: true, InEbookLibrary: true, EbookMonitored: true}
	if err := store.UpsertBook(f.book); err != nil {
		t.Fatal(err)
	}
	f.owned = &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "2",
		Title: "Sourcery", Monitored: true, InEbookLibrary: true, EbookMonitored: true}
	if err := store.UpsertBook(f.owned); err != nil {
		t.Fatal(err)
	}
	// Give Sourcery a file so it isn't wanted.
	rootDir := t.TempDir()
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, rootDir); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.owned.ID, Path: filepath.Join(rootDir, "s.epub"), Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}

	f.svc = New(store, indexers, downloads)
	return f
}

const goodSearchXML = `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
  <item><title>Terry Pratchett - Mort Retail EPUB</title><guid>g1</guid>
    <link>https://idx/get/mort.nzb</link><newznab:attr name="size" value="1048576"/></item>
  <item><title>Terry Pratchett - Mort PDF</title><guid>g2</guid>
    <link>https://idx/get/mort-pdf.nzb</link><newznab:attr name="size" value="1048576"/></item>
  <item><title>Stephen King - It EPUB</title><guid>g3</guid>
    <link>https://idx/get/it.nzb</link><newznab:attr name="size" value="1048576"/></item>
</channel></rss>`

func TestSearchWantedGrabsBest(t *testing.T) {
	f := fixture(t, goodSearchXML)

	outcomes, err := f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatalf("SearchWanted: %v", err)
	}
	// Only Mort is wanted (Sourcery has a file).
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	o := outcomes[0]
	if !o.Grabbed || o.Release != "Terry Pratchett - Mort Retail EPUB" || o.Client != "sab" {
		t.Fatalf("outcome = %+v", o)
	}
	// The best (retail epub) URL went to the client.
	if len(f.sabAdd) != 1 || f.sabAdd[0] != "https://idx/get/mort.nzb" {
		t.Errorf("sab adds = %v", f.sabAdd)
	}
	// Grab recorded against the book.
	grabs, _ := f.grabs.ListGrabs(download.GrabStatusGrabbed)
	if len(grabs) != 1 || grabs[0].BookID != f.book.ID {
		t.Fatalf("grabs = %+v", grabs)
	}

	// Second pass: pending grab, no re-search.
	outcomes, err = f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("re-search happened: %+v", outcomes)
	}
	if len(f.sabAdd) != 1 {
		t.Errorf("double grab: %v", f.sabAdd)
	}
}

func TestSearchBookNoApprovedCandidates(t *testing.T) {
	// Indexer only offers the wrong book.
	f := fixture(t, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
	  <item><title>Stephen King - It EPUB</title><guid>g3</guid>
	    <link>https://idx/get/it.nzb</link><newznab:attr name="size" value="1048576"/></item>
	</channel></rss>`)

	outcome, err := f.svc.SearchBook(context.Background(), f.book.ID, "ebook")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Grabbed || !strings.Contains(outcome.Message, "no approved release") {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(f.sabAdd) != 0 {
		t.Errorf("grabbed despite no approved candidates: %v", f.sabAdd)
	}
}

func TestSearchWantedAudiobook(t *testing.T) {
	// Indexer serves an m4b for Mort; Mort has a monitored audiobook edition
	// and already owns the ebook, so only the audiobook should be searched.
	f := fixture(t, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
	  <item><title>Terry Pratchett - Mort Unabridged M4B</title><guid>a1</guid>
	    <link>https://idx/get/mort.m4b.nzb</link><newznab:attr name="size" value="209715200"/></item>
	</channel></rss>`)

	// Mort owns its ebook already.
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook", Path: "/x/mort.epub", Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}
	// Opt in via audiobook library membership (Plex-style explicit model).
	if err := f.store.SetBookLibrary(f.book.ID, "audiobook", true, true); err != nil {
		t.Fatal(err)
	}

	outcomes, err := f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	o := outcomes[0]
	if !o.Grabbed || o.MediaType != "audiobook" || o.Release != "Terry Pratchett - Mort Unabridged M4B" {
		t.Fatalf("outcome = %+v", o)
	}
	grabs, _ := f.grabs.ListGrabs(download.GrabStatusGrabbed)
	if len(grabs) != 1 || grabs[0].MediaType != "audiobook" {
		t.Fatalf("grabs = %+v", grabs)
	}

	// Without audiobook membership (Sourcery), nothing else runs.
	for _, x := range outcomes {
		if x.BookID == f.owned.ID {
			t.Error("book without audiobook membership was searched")
		}
	}
}

func TestSearchWantedMagazine(t *testing.T) {
	// Two issues on the indexer; one already in the library.
	f := fixture(t, `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
	  <item><title>The Economist - 2026-07-04 PDF</title><guid>m1</guid>
	    <link>https://idx/get/eco-0704.nzb</link><newznab:attr name="size" value="52428800"/></item>
	  <item><title>The Economist - 2026-06-27 PDF</title><guid>m2</guid>
	    <link>https://idx/get/eco-0627.nzb</link><newznab:attr name="size" value="52428800"/></item>
	  <item><title>The Economist - 2026-07-04 EPUB</title><guid>m3</guid>
	    <link>https://idx/get/eco-0704-epub.nzb</link><newznab:attr name="size" value="52428800"/></item>
	</channel></rss>`)

	// Give the ebook fixtures files so only the magazine is wanted.
	for _, b := range []*library.Book{f.book} {
		if err := f.store.UpsertBookFile(&library.BookFile{
			RootFolderID: 1, BookID: b.ID, MediaType: "ebook", Path: "/x/" + b.Title + ".epub", Format: "epub",
		}); err != nil {
			t.Fatal(err)
		}
	}

	mag := &library.Series{Source: "manual", ForeignID: "magazine:economist",
		Title: "The Economist", MediaType: "magazine", Monitored: true, MonitorNew: true}
	if err := f.store.UpsertSeries(mag); err != nil {
		t.Fatal(err)
	}
	// 2026-06-27 is already owned.
	if _, err := f.store.CreateMagazineIssue(mag, "2026-06-27", false); err != nil {
		t.Fatal(err)
	}

	// Magazines are organize-only for now: even with grabbable issues on the
	// indexer and a monitored magazine, the sweep must not search, grab, or
	// materialize anything for it.
	outcomes, err := f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if o.MediaType == "magazine" {
			t.Fatalf("magazine swept despite acquisition being disabled: %+v", o)
		}
	}
	if len(f.sabAdd) != 0 {
		t.Errorf("sab adds = %v, want none", f.sabAdd)
	}
	// Only the pre-existing issue remains; nothing was materialized.
	volumes, _ := f.store.ListVolumes(mag.ID)
	if len(volumes) != 1 {
		t.Fatalf("volumes = %+v, want just the owned issue", volumes)
	}
}

func TestBlocklistedReleaseIsSkipped(t *testing.T) {
	f := fixture(t, goodSearchXML)

	// The best release (retail epub, guid g1) failed before; search must
	// fall to the next approved candidate.
	if err := f.grabs.AddBlock("g1", "Terry Pratchett - Mort Retail EPUB", "test"); err != nil {
		t.Fatal(err)
	}

	outcomes, err := f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Grabbed {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if outcomes[0].Release != "Terry Pratchett - Mort PDF" {
		t.Fatalf("grabbed %q, want the non-blocked PDF", outcomes[0].Release)
	}
}

func TestUpgradeSearch(t *testing.T) {
	f := fixture(t, goodSearchXML)

	// Mort owns a PDF; with upgrades off, nothing is wanted.
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook",
		Path: "/x/mort.pdf", Format: "pdf",
	}); err != nil {
		t.Fatal(err)
	}
	outcomes, err := f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("upgrades off but searched: %+v", outcomes)
	}

	// Enable upgrades on the default ebook profile: the pdf is below the
	// epub cutoff, so the book is wanted again — and only the epub approves.
	profile, err := f.store.DefaultProfile("ebook")
	if err != nil {
		t.Fatal(err)
	}
	profile.UpgradesAllowed = true
	if err := f.store.UpdateProfile(profile); err != nil {
		t.Fatal(err)
	}

	outcomes, err = f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].Grabbed {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if outcomes[0].Release != "Terry Pratchett - Mort Retail EPUB" {
		t.Fatalf("grabbed %q, want the epub upgrade", outcomes[0].Release)
	}

	// Owning the cutoff format ends the upgrade hunt.
	files, _ := f.store.ListBookFiles(f.book.ID)
	for _, file := range files {
		if err := f.store.DeleteBookFile(file.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook",
		Path: "/x/mort.epub", Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}
	// Clear the pending grab from the upgrade above so it can't mask wants.
	grabs, _ := f.grabs.ListGrabs(download.GrabStatusGrabbed)
	for _, g := range grabs {
		if err := f.grabs.ResolveGrab(g.ID, download.GrabStatusImported, "test"); err != nil {
			t.Fatal(err)
		}
	}
	outcomes, err = f.svc.SearchWanted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("cutoff met but still searched: %+v", outcomes)
	}
}

func TestSearchBookPendingGrabShortCircuits(t *testing.T) {
	f := fixture(t, goodSearchXML)

	if _, err := f.svc.SearchBook(context.Background(), f.book.ID, "ebook"); err != nil {
		t.Fatal(err)
	}
	outcome, err := f.svc.SearchBook(context.Background(), f.book.ID, "ebook")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Grabbed || !strings.Contains(outcome.Message, "already pending") {
		t.Fatalf("outcome = %+v", outcome)
	}
}

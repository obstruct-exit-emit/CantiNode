package libgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/indexer"
)

// resultsHTML mirrors the current libgen.li results table: the title is the
// first edition.php anchor, and the author is the plain <td> right after the
// title cell (the site no longer wraps authors in author.php links). Size lives
// in the file.php cell and the md5 rides the /ads.php download link.
const resultsHTML = `<table id="tablelibgen"><tbody>
<tr>
  <td><b>Dune Chronicles <br>Dune Universe 7</b><br>
    <a title="Add/Edit" href="edition.php?id=2719411">Hunters of Dune <i></i></a>
    <nobr><span class="badge badge-primary">b</span></nobr></td>
  <td>Herbert, Brian; Anderson, Kevin J</td>
  <td>Herbert Properties LLC</td>
  <td><nobr>2006</nobr></td>
  <td>English</td>
  <td>0</td>
  <td><nobr><a href="/file.php?id=3068129">990 kB</a></nobr></td>
  <td>fb2</td>
  <td><a title="libgen" href="/ads.php?md5=8fdb106f421adb411735aa99d746a037"><span class="badge">Libgen</span></a></td>
</tr>
<tr>
  <td><b><a href="series.php?id=1">Earthsea</a> №1</b><br>
    <a href="edition.php?id=999">A Wizard of Earthsea <i></i></a>
    <a href="edition.php?id=999"><i><font color="green">9780553380163</font></i></a></td>
  <td>Le Guin, Ursula K. (Author)</td>
  <td>Parnassus</td><td>1968</td><td>English</td><td>0</td>
  <td><nobr><a href="/file.php?id=999">1.2 MB</a></nobr></td>
  <td>epub</td>
  <td><a href="/ads.php?md5=aa110dd200902c872d94c890e2a2c221"><span>Libgen</span></a></td>
</tr>
</tbody></table>`

func TestParseResults(t *testing.T) {
	res := parseResults(resultsHTML)
	if len(res) != 2 {
		t.Fatalf("parsed %d rows, want 2: %+v", len(res), res)
	}
	r0 := res[0]
	// Title is the edition.php text, NOT the author anchor.
	if r0.Title != "Hunters of Dune" {
		t.Errorf("row0 title = %q, want Hunters of Dune", r0.Title)
	}
	if r0.MD5 != "8fdb106f421adb411735aa99d746a037" {
		t.Errorf("row0 md5 = %q", r0.MD5)
	}
	if r0.Size != int64(990*1024) {
		t.Errorf("row0 size = %d, want 990 kB", r0.Size)
	}
	// Structured columns: author (from the plain cell after the title), year,
	// language, format.
	if r0.Authors != "Herbert, Brian; Anderson, Kevin J" {
		t.Errorf("row0 authors = %q", r0.Authors)
	}
	if r0.Year != "2006" || r0.Language != "english" || r0.Format != "fb2" {
		t.Errorf("row0 year/lang/format = %q/%q/%q", r0.Year, r0.Language, r0.Format)
	}
	// The scene-like name carries everything the scorer needs.
	if got := r0.releaseName(); got != "Herbert, Brian; Anderson, Kevin J - Hunters of Dune (2006) english fb2" {
		t.Errorf("row0 releaseName = %q", got)
	}
	// The role suffix is stripped from a plain author cell too.
	if res[1].Authors != "Le Guin, Ursula K." {
		t.Errorf("row1 authors = %q", res[1].Authors)
	}
	// Second row: title is the first edition.php link, not the ISBN one.
	if res[1].Title != "A Wizard of Earthsea" || res[1].Format != "epub" || res[1].Language != "english" {
		t.Errorf("row1 = %+v", res[1])
	}
	if res[1].Size != 1258291 { // 1.2 MB truncated
		t.Errorf("row1 size = %d, want ~1.2 MB", res[1].Size)
	}
}

func TestMirrorDownloadURLs(t *testing.T) {
	got := MirrorDownloadURLs("ABCDEF0123456789ABCDEF0123456789")
	want := "https://libgen.li/ads.php?md5=abcdef0123456789abcdef0123456789"
	if got != want {
		t.Errorf("MirrorDownloadURLs = %q, want %q", got, want)
	}
}

// TestSearch: the real Search queries index.php, dedupes by md5, and builds an
// ads.php download URL on the serving host for each release.
func TestSearch(t *testing.T) {
	var searchHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/index.php") {
			searchHits++
			_, _ = w.Write([]byte(resultsHTML))
			return
		}
		_, _ = w.Write([]byte("<html>libgen</html>"))
	}))
	defer srv.Close()

	def := Def()
	if def.Name != "libgen" || def.Protocol != indexer.ProtocolDirect || !def.Serves("ebook") || def.Serves("audiobook") {
		t.Fatalf("def = %+v", def)
	}

	s := def.New(&indexer.Indexer{Name: "LG", BaseURL: srv.URL}, srv.Client())
	rels, err := s.Search(context.Background(), "dune", "ebook")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("releases = %d, want 2: %+v", len(rels), rels)
	}
	if rels[0].DownloadURL != srv.URL+"/ads.php?md5=8fdb106f421adb411735aa99d746a037" {
		t.Errorf("download URL = %q, want ads.php on the serving host", rels[0].DownloadURL)
	}
	if rels[0].Protocol != indexer.ProtocolDirect {
		t.Errorf("protocol = %q", rels[0].Protocol)
	}
	if err := s.Test(context.Background()); err != nil {
		t.Errorf("Test: %v", err)
	}
	if got, err := s.Search(context.Background(), "x", "audiobook"); err != nil || got != nil {
		t.Errorf("audiobook search = %v, %v; want nil, nil", got, err)
	}
}

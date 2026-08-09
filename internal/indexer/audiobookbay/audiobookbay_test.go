package audiobookbay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/indexer"
)

const listingHTML = `
<html><body>
  <div class="post">
    <div class="postTitle"><h2><a href="/audio-books/the-hobbit-unabridged/">The Hobbit (Unabridged) [MP3]</a></h2></div>
  </div>
  <div class="post">
    <div class="postTitle"><h2><a href="https://audiobookbay.lu/audio-books/dune-frank-herbert/">Dune &#8211; Frank Herbert</a></h2></div>
  </div>
</body></html>`

const detailHTML = `
<html><body>
  <h1 class="postTitle">The Hobbit (Unabridged)</h1>
  <p>Format: MP3<br>Bitrate: 128 Kbps<br>File Size: 512.5 MBs</p>
  <table>
    <tr><td>Info Hash:</td><td>0123456789ABCDEF0123456789ABCDEF01234567</td></tr>
    <tr><td>Tracker:</td><td>udp://tracker.openbittorrent.com:80/announce</td></tr>
    <tr><td></td><td>udp://tracker.opentrackr.org:1337/announce</td></tr>
    <tr><td></td><td>http://tracker.example.org/announce.php?x=1</td></tr>
  </table>
</body></html>`

func TestParseListing(t *testing.T) {
	posts := parseListing(listingHTML, "https://audiobookbay.lu")
	if len(posts) != 2 {
		t.Fatalf("parsed %d posts, want 2: %+v", len(posts), posts)
	}
	// ABB's canonical permalinks keep their trailing slash — the slash-less form
	// 301s with no Location header, breaking the grab-time Resolve fetch.
	if posts[0].URL != "https://audiobookbay.lu/audio-books/the-hobbit-unabridged/" {
		t.Errorf("post[0].URL = %q", posts[0].URL)
	}
	if posts[0].Title != "The Hobbit (Unabridged) [MP3]" {
		t.Errorf("post[0].Title = %q", posts[0].Title)
	}
	// Entity-decoded and absolute already.
	if posts[1].Title != "Dune – Frank Herbert" {
		t.Errorf("post[1].Title = %q", posts[1].Title)
	}
	if posts[1].URL != "https://audiobookbay.lu/audio-books/dune-frank-herbert/" {
		t.Errorf("post[1].URL = %q", posts[1].URL)
	}
}

// TestParseListingKeywordsPerPost: a post's Keywords tag list — which can
// name the author even when the title alone doesn't (e.g. a bare "Dune
// Messiah" post) — must be scoped to that post's own segment, not leak from
// or into a neighboring post's Keywords.
func TestParseListingKeywordsPerPost(t *testing.T) {
	html := `<div class="post"><div class="postTitle"><h2><a href="/abss/the-dune-saga/" rel="bookmark">The Dune Saga - All Six Books</a></h2></div><div class="postInfo">Category: Sci-Fi&nbsp; <br />Language: English<span style="margin-left:100px;">Keywords: Arrakis&nbsp Politics&nbsp Frank Herbert&nbsp </span><br /></div><div class="postContent">stuff</div></div>
<div class="post"><div class="postTitle"><h2><a href="/abss/dune-xmessiah/" rel="bookmark">Dune Messiah</a></h2></div><div class="postInfo">Category: Sci-Fi&nbsp; <br />Language: English<span style="margin-left:100px;">Keywords: Dune&nbsp Frank Herbert&nbsp </span><br /></div><div class="postContent">stuff</div></div>`

	posts := parseListing(html, "https://audiobookbay.lu")
	if len(posts) != 2 {
		t.Fatalf("parsed %d posts, want 2: %+v", len(posts), posts)
	}
	if posts[0].Keywords != "Arrakis Politics Frank Herbert" {
		t.Errorf("post[0].Keywords = %q", posts[0].Keywords)
	}
	if posts[1].Keywords != "Dune Frank Herbert" {
		t.Errorf("post[1].Keywords = %q", posts[1].Keywords)
	}
}

// TestParseListingSizeAndPostedDate: a post's file size and posted date live
// in the same .postContent block as its Keywords, on the listing page itself
// — no detail-page fetch needed. Two posts with different values confirm
// per-post scoping, and a single-digit day ("7 Mar") confirms the date parse
// isn't only accidentally correct for zero-padded days.
func TestParseListingSizeAndPostedDate(t *testing.T) {
	html := `<div class="post"><div class="postTitle"><h2><a href="/abss/a/" rel="bookmark">Book A</a></h2></div><div class="postInfo">Language: English<span style="margin-left:100px;">Keywords: Author A</span><br /></div><div class="postContent"><p style='text-align:center;'>Posted: 27 Mar 2008<br />Format: <span>MP3</span> / Bitrate: <span>128 Kbps</span><br />File Size: <span style='color:#00f;'>204.13</span> MBs</p></div></div>
<div class="post"><div class="postTitle"><h2><a href="/abss/b/" rel="bookmark">Book B</a></h2></div><div class="postInfo">Language: English<span style="margin-left:100px;">Keywords: Author B</span><br /></div><div class="postContent"><p style='text-align:center;'>Posted: 7 Jan 2015<br />File Size: <span style='color:#00f;'>1.5</span> GBs</p></div></div>`

	posts := parseListing(html, "https://audiobookbay.lu")
	if len(posts) != 2 {
		t.Fatalf("parsed %d posts, want 2: %+v", len(posts), posts)
	}
	mb := 204.13
	if want := int64(mb * (1 << 20)); posts[0].Size != want {
		t.Errorf("post[0].Size = %d, want %d", posts[0].Size, want)
	}
	if posts[0].PostedDate.Format("2006-01-02") != "2008-03-27" {
		t.Errorf("post[0].PostedDate = %v, want 2008-03-27", posts[0].PostedDate)
	}
	gb := 1.5
	if want := int64(gb * (1 << 30)); posts[1].Size != want {
		t.Errorf("post[1].Size = %d, want %d", posts[1].Size, want)
	}
	if posts[1].PostedDate.Format("2006-01-02") != "2015-01-07" {
		t.Errorf("post[1].PostedDate = %v, want 2015-01-07 (single-digit day)", posts[1].PostedDate)
	}
}

func TestParseDetailAndMagnet(t *testing.T) {
	hash, trackers, size, ok := parseDetail(detailHTML)
	if !ok {
		t.Fatal("expected a parseable detail page")
	}
	if hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("hash = %q (want lowercased)", hash)
	}
	if len(trackers) != 3 {
		t.Fatalf("trackers = %v, want 3", trackers)
	}
	if size != int64(512.5*(1<<20)) {
		t.Errorf("size = %d, want %d", size, int64(512.5*(1<<20)))
	}

	magnet := buildMagnet(hash, "The Hobbit (Unabridged)", trackers)
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("magnet prefix wrong: %q", magnet)
	}
	if !strings.Contains(magnet, "dn=The%20Hobbit%20%28Unabridged%29") {
		t.Errorf("magnet missing display name: %q", magnet)
	}
	if strings.Count(magnet, "&tr=") != 3 {
		t.Errorf("magnet should carry 3 trackers: %q", magnet)
	}
}

func TestParseDetailNoHash(t *testing.T) {
	if _, _, _, ok := parseDetail("<html>no info hash here</html>"); ok {
		t.Error("a page without an info hash must not parse as a release")
	}
}

func TestParseBases(t *testing.T) {
	cases := map[string][]string{
		"":                                      {DefaultBaseURL},
		"https://a.example/":                    {"https://a.example"},
		"https://a.example, https://b.example/": {"https://a.example", "https://b.example"},
		" , https://only.example":               {"https://only.example"},
	}
	for in, want := range cases {
		got := ParseBases(in)
		if len(got) != len(want) {
			t.Errorf("ParseBases(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseBases(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestMagnetEscapeUsesPercent20(t *testing.T) {
	magnet := buildMagnet("0123456789abcdef0123456789abcdef01234567", "A Title With Spaces", []string{"udp://t.example:80/announce"})
	if strings.Contains(magnet, "+") {
		t.Errorf("magnet must not use '+' for spaces: %q", magnet)
	}
	if !strings.Contains(magnet, "dn=A%20Title%20With%20Spaces") {
		t.Errorf("magnet dn should be %%20-escaped: %q", magnet)
	}
}

func TestParseListingSkipsNavLinks(t *testing.T) {
	html := `
	<a href="/audio-books/type/fiction/">Fiction</a>
	<a href="/audio-books/page/2/">2</a>
	<a href="/audio-books/the-real-book/">The Real Book</a>`
	posts := parseListing(html, "https://abb.example")
	if len(posts) != 1 || posts[0].Title != "The Real Book" {
		t.Fatalf("posts = %+v, want only The Real Book", posts)
	}
}

// abbServer serves ABB's shape: a homepage (session warm-up), a search listing,
// and detail pages. It counts requests by kind so tests can assert search never
// crawls detail pages.
type abbServer struct {
	*httptest.Server
	homeHits, searchHits, detailHits int
}

func newABBServer(t *testing.T, listing string) *abbServer {
	t.Helper()
	a := &abbServer{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("s") != "":
			a.searchHits++
			_, _ = w.Write([]byte(listing))
		case strings.Contains(r.URL.Path, "/audio-books/"):
			a.detailHits++
			_, _ = w.Write([]byte(detailHTML))
		default:
			a.homeHits++
			_, _ = w.Write([]byte("<html>abb home</html>"))
		}
	}))
	t.Cleanup(a.Close)
	return a
}

const oneResultListing = `<div class="postTitle"><a href="/audio-books/the-hobbit/">The Hobbit</a></div>`

// TestSearchLowercasesQuery: ABB's edge redirects a ?s= value starting with
// an uppercase letter to the homepage (confirmed against the live site) —
// and book titles are naturally Title Case ("Dune Messiah"), so an
// unlowercased query would bounce nearly every real search. The listing
// request must always carry a lowercased query.
func TestSearchLowercasesQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := r.URL.Query().Get("s"); s != "" {
			gotQuery = s
			_, _ = w.Write([]byte(oneResultListing))
			return
		}
		_, _ = w.Write([]byte("<html>abb home</html>"))
	}))
	defer srv.Close()

	// "The Hobbit" is Title Case (leading uppercase, the shape ABB's edge
	// bounces) and matches the fixture listing, so the search also passes the
	// relevance guard once lowercased.
	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	if _, err := s.Search(context.Background(), "The Hobbit", "audiobook"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "the hobbit" {
		t.Errorf("search query sent to ABB = %q, want lowercased %q", gotQuery, "the hobbit")
	}
}

// TestSearchDefersDetailFetch: a search returns releases whose download URL is
// the release page (not a magnet), and it never fetches a detail page — the
// magnet is assembled only when Resolve is called at grab time.
func TestSearchDefersDetailFetch(t *testing.T) {
	srv := newABBServer(t, oneResultListing)

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	releases, err := s.Search(context.Background(), "hobbit", "audiobook")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %+v, want 1", releases)
	}
	if releases[0].DownloadURL != srv.URL+"/audio-books/the-hobbit/" {
		t.Errorf("download URL = %q, want the release page", releases[0].DownloadURL)
	}
	if srv.detailHits != 0 {
		t.Errorf("search fetched %d detail page(s); it must fetch none", srv.detailHits)
	}
	if srv.homeHits == 0 {
		t.Error("search should warm up the session by hitting the homepage")
	}

	// Resolve (grab time) fetches exactly the one detail page and builds the magnet.
	r, ok := s.(indexer.Resolver)
	if !ok {
		t.Fatal("ABB searcher must implement indexer.Resolver")
	}
	magnet, err := r.Resolve(context.Background(), releases[0].DownloadURL)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:0123456789abcdef") {
		t.Errorf("resolved magnet = %q", magnet)
	}
	if srv.detailHits != 1 {
		t.Errorf("Resolve fetched %d detail page(s), want 1", srv.detailHits)
	}
}

// TestSearchFailsOverToFallbackURL: the primary site is down; the search must
// succeed transparently through the fallback mirror.
func TestSearchFailsOverToFallbackURL(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	mirror := newABBServer(t, oneResultListing)

	def := Def()
	s := def.New(&indexer.Indexer{Name: "ABB", BaseURL: dead.URL + "," + mirror.URL}, mirror.Client())
	releases, err := s.Search(context.Background(), "hobbit", "audiobook")
	if err != nil {
		t.Fatalf("Search with fallback: %v", err)
	}
	if len(releases) != 1 || releases[0].DownloadURL != mirror.URL+"/audio-books/the-hobbit/" {
		t.Fatalf("releases = %+v", releases)
	}
	if err := s.Test(context.Background()); err != nil {
		t.Errorf("Test with fallback: %v", err)
	}
	deadOnly := def.New(&indexer.Indexer{Name: "ABB", BaseURL: dead.URL}, dead.Client())
	if err := deadOnly.Test(context.Background()); err == nil {
		t.Error("Test against only a dead site should fail")
	}
}

// TestSearchDetectsHomepageBlock: a search redirected to the homepage (ABB's
// rate-limit/block behaviour) surfaces as an error, not empty success.
func TestSearchDetectsHomepageBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") != "" {
			http.Redirect(w, r, "/", http.StatusFound) // block → bounce to home
			return
		}
		_, _ = w.Write([]byte("<html>abb home</html>"))
	}))
	defer srv.Close()

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	_, err := s.Search(context.Background(), "blocked", "audiobook")
	if err == nil || !strings.Contains(err.Error(), "homepage") {
		t.Errorf("expected a homepage-block error, got %v", err)
	}
}

// TestSearchRetriesEmptyListing: ABB can bounce a search with an empty body
// instead of a homepage redirect. That must be retried the same as a
// redirect, not treated as a (zero-result) success.
func TestSearchRetriesEmptyListing(t *testing.T) {
	searchHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") == "" {
			_, _ = w.Write([]byte("<html>abb home</html>"))
			return
		}
		searchHits++
		if searchHits == 1 {
			return // empty body, HTTP 200 — the throttle signature
		}
		_, _ = w.Write([]byte(oneResultListing))
	}))
	defer srv.Close()

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	releases, err := s.Search(context.Background(), "hobbit", "audiobook")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %+v, want 1", releases)
	}
	if searchHits != 2 {
		t.Errorf("search requests = %d, want 2 (one retry)", searchHits)
	}
}

// homepageFeedListing is what ABB serves at the search URL when it soft-blocks a
// throttled IP: a real-looking listing of its "Latest" posts, HTTP 200 and no
// redirect, none of them related to the query. Only a relevance check catches it.
const homepageFeedListing = `<div class="postTitle"><a href="/audio-books/atomic-habits/">Atomic Habits</a></div>` +
	`<div class="postTitle"><a href="/audio-books/the-silent-patient/">The Silent Patient</a></div>`

// TestSearchDetectsHomepageFeedSoftBlock: a throttled ABB answers the search
// with HTTP 200 and its unrelated homepage feed (never a redirect). The search
// must reject it as a block, not hand back the feed's posts as bogus matches.
func TestSearchDetectsHomepageFeedSoftBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") != "" {
			_, _ = w.Write([]byte(homepageFeedListing))
			return
		}
		_, _ = w.Write([]byte("<html>abb home</html>"))
	}))
	defer srv.Close()

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	releases, err := s.Search(context.Background(), "dune messiah", "audiobook")
	if err == nil {
		t.Fatalf("expected a soft-block error, got %d releases", len(releases))
	}
	if !strings.Contains(err.Error(), "unrelated") {
		t.Errorf("error = %v, want it to mention unrelated results", err)
	}
}

// TestSearchRecoversFromHomepageFeed: the unrelated feed on the first attempt,
// real results on the retry — the fresh-session retry must recover it, exactly
// like the empty-page and redirect cases.
func TestSearchRecoversFromHomepageFeed(t *testing.T) {
	searchHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("s") == "" {
			_, _ = w.Write([]byte("<html>abb home</html>"))
			return
		}
		searchHits++
		if searchHits == 1 {
			_, _ = w.Write([]byte(homepageFeedListing))
			return
		}
		_, _ = w.Write([]byte(`<div class="postTitle"><a href="/audio-books/dune-messiah/">Dune Messiah</a></div>`))
	}))
	defer srv.Close()

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	releases, err := s.Search(context.Background(), "dune messiah", "audiobook")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %+v, want 1 (recovered on retry)", releases)
	}
	if searchHits != 2 {
		t.Errorf("search requests = %d, want 2 (one retry)", searchHits)
	}
}

// TestResolveRetriesEmptyDetailPage: the grab-time detail fetch can hit the
// same blank-page throttle signature as a search. One retry on a fresh
// session should recover it instead of failing the grab outright.
func TestResolveRetriesEmptyDetailPage(t *testing.T) {
	detailHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/audio-books/") {
			detailHits++
			if detailHits == 1 {
				return // empty body on the first attempt
			}
			_, _ = w.Write([]byte(detailHTML))
			return
		}
		_, _ = w.Write([]byte("<html>abb home</html>"))
	}))
	defer srv.Close()

	s := Def().New(&indexer.Indexer{Name: "ABB", BaseURL: srv.URL}, srv.Client())
	r := s.(indexer.Resolver)
	magnet, err := r.Resolve(context.Background(), srv.URL+"/audio-books/the-hobbit/")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:0123456789abcdef") {
		t.Errorf("resolved magnet = %q", magnet)
	}
	if detailHits != 2 {
		t.Errorf("detail requests = %d, want 2 (one retry)", detailHits)
	}
}

// TestParseDetailWhitespaceHash: ABB sometimes splits the info hash across
// whitespace/markup within the table cell; it must still be read as one
// contiguous hash.
func TestParseDetailWhitespaceHash(t *testing.T) {
	html := `<table><tr><td>Info Hash</td><td>0123 4567 89AB CDEF 0123 4567 89AB CDEF 0123 4567</td></tr></table>`
	hash, _, _, ok := parseDetail(html)
	if !ok || hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("parseDetail whitespace hash = (%q, %v)", hash, ok)
	}
}

// TestParseDetailSHA256Hash: a v2/hybrid release lists a 64-hex info hash
// instead of the usual 40-hex SHA1; it must be accepted, not rejected as
// malformed.
func TestParseDetailSHA256Hash(t *testing.T) {
	hash64 := strings.Repeat("ab", 32) // 64 hex chars
	html := `<table><tr><td>Info Hash</td><td>` + hash64 + `</td></tr></table>`
	hash, _, _, ok := parseDetail(html)
	if !ok || hash != hash64 {
		t.Fatalf("parseDetail 64-char hash = (%q, %v)", hash, ok)
	}
}

// TestParseDetailMagnetFallback: when the Info Hash table cell is missing or
// unparsable, a complete magnet link elsewhere on the page is a valid
// fallback source for the hash.
func TestParseDetailMagnetFallback(t *testing.T) {
	html := `<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=x">magnet</a>`
	hash, _, _, ok := parseDetail(html)
	if !ok || hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("parseDetail magnet fallback = (%q, %v)", hash, ok)
	}
}

// TestDefRegistersAsTorrentAudiobook: the definition advertises what the
// framework and UI rely on.
func TestDef(t *testing.T) {
	def := Def()
	if def.Name != "audiobookbay" || def.Protocol != indexer.ProtocolTorrent {
		t.Errorf("def = %+v", def)
	}
	if !def.Serves("audiobook") || def.Serves("ebook") {
		t.Error("ABB should serve audiobook only")
	}
	if def.DefaultBaseURL == "" || def.New == nil {
		t.Error("def needs a default base URL and constructor")
	}
	// A native searcher that isn't asked for audiobooks returns nothing.
	s := def.New(&indexer.Indexer{Name: "ABB"}, nil)
	if rels, err := s.Search(nil, "anything", "ebook"); err != nil || rels != nil {
		t.Errorf("non-audiobook search = (%v, %v), want (nil, nil)", rels, err)
	}
}

package qbittorrent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeServer is a minimal stand-in for a qBittorrent-Web-API-compatible
// server (real qBittorrent, or AcerviNode's own compat shim) — just
// enough surface for this package's own client code to exercise against.
type fakeServer struct {
	t         *testing.T
	username  string
	password  string
	validSIDs map[string]bool

	torrents map[string]*fakeTorrent // by hash

	// loginCount lets a test assert how many times the client actually
	// logged in (e.g. once normally, twice across a simulated session
	// expiry).
	loginCount int
}

type fakeTorrent struct {
	hash        string
	category    string
	state       string // qBittorrent state vocabulary, e.g. "downloading", "pausedUP", "error"
	contentPath string
}

// newFakeServer requires both username and password to match — unlike
// AcerviNode's own compat shim (any username, only password checked),
// this fake matches real qBittorrent's actual behavior, since that's the
// stricter case this package needs to work against.
func newFakeServer(t *testing.T, username, password string) *fakeServer {
	return &fakeServer{
		t:         t,
		username:  username,
		password:  password,
		validSIDs: map[string]bool{},
		torrents:  map[string]*fakeTorrent{},
	}
}

func (f *fakeServer) start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.handle))
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v2/auth/login":
		f.handleLogin(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v2/torrents/"):
		f.handleTorrentShim(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f.loginCount++
	if r.FormValue("username") != f.username || r.FormValue("password") != f.password {
		w.Write([]byte("Fails."))
		return
	}
	sid := "sid-" + strconv.Itoa(len(f.validSIDs)+1)
	f.validSIDs[sid] = true
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid})
	w.Write([]byte("Ok."))
}

func (f *fakeServer) sessionValid(r *http.Request) bool {
	ck, err := r.Cookie("SID")
	if err != nil {
		return false
	}
	return f.validSIDs[ck.Value]
}

func (f *fakeServer) handleTorrentShim(w http.ResponseWriter, r *http.Request) {
	if !f.sessionValid(r) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Forbidden"))
		return
	}

	switch {
	case r.URL.Path == "/api/v2/torrents/add" && r.Method == http.MethodPost:
		f.handleAddTorrent(w, r)
	case r.URL.Path == "/api/v2/torrents/info":
		f.handleTorrentsInfo(w, r)
	case r.URL.Path == "/api/v2/torrents/delete" && r.Method == http.MethodPost:
		f.handleDeleteTorrent(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeServer) handleDeleteTorrent(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	for _, h := range strings.Split(r.FormValue("hashes"), "|") {
		delete(f.torrents, h)
	}
	w.Write([]byte("Ok."))
}

func (f *fakeServer) handleAddTorrent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil && err != http.ErrNotMultipart {
		w.Write([]byte("Fails."))
		return
	}
	category := r.FormValue("category")

	if urls := r.FormValue("urls"); urls != "" {
		hash, err := magnetInfoHash(strings.TrimSpace(urls))
		if err != nil {
			w.Write([]byte("Fails."))
			return
		}
		f.torrents[hash] = &fakeTorrent{hash: hash, category: category, state: "downloading"}
		w.Write([]byte("Ok."))
		return
	}

	if r.MultipartForm != nil && len(r.MultipartForm.File["torrents"]) > 0 {
		header := r.MultipartForm.File["torrents"][0]
		file, err := header.Open()
		if err != nil {
			w.Write([]byte("Fails."))
			return
		}
		data, _ := io.ReadAll(file)
		file.Close()
		// The fake doesn't parse real bencoded torrent data — it derives
		// a fake but stable hash from the uploaded bytes, which is all
		// this package's own tests need (they just check *some* new hash
		// appears).
		hash := "filehash-" + strconv.Itoa(len(data)) + "-" + strconv.Itoa(len(f.torrents))
		f.torrents[hash] = &fakeTorrent{hash: hash, category: category, state: "downloading"}
		w.Write([]byte("Ok."))
		return
	}

	w.Write([]byte("Fails."))
}

func (f *fakeServer) handleTorrentsInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wantHashes := map[string]bool{}
	if h := q.Get("hashes"); h != "" {
		for _, part := range strings.Split(h, "|") {
			wantHashes[part] = true
		}
	}
	wantCategory := q.Get("category")

	type item struct {
		Hash        string `json:"hash"`
		State       string `json:"state"`
		ContentPath string `json:"content_path"`
	}
	var out []item
	for _, tr := range f.torrents {
		if len(wantHashes) > 0 && !wantHashes[tr.hash] {
			continue
		}
		if wantCategory != "" && tr.category != wantCategory {
			continue
		}
		out = append(out, item{Hash: tr.hash, State: tr.state, ContentPath: tr.contentPath})
	}
	if out == nil {
		out = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

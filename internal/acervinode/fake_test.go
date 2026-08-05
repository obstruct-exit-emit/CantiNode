package acervinode

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeServer is a minimal stand-in for AcerviNode's own qBittorrent and
// SABnzbd shims — just enough surface for this package's own client
// code to exercise against, built from the exact contracts documented in
// AcerviNode's docs/qbittorrent-api.md and docs/sabnzbd-api.md (see this
// package's own research notes) rather than guessed.
type fakeServer struct {
	t         *testing.T
	apiKey    string
	validSIDs map[string]bool

	torrents map[string]*fakeTorrent // by hash
	nzbs     map[string]*fakeNZB     // by nzo_id
	nextNzo  int

	// loginCount lets a test assert how many times the client actually
	// logged in (e.g. once normally, twice across a simulated session
	// expiry).
	loginCount int
}

type fakeTorrent struct {
	hash        string
	category    string
	state       string // qBittorrent-shim vocabulary, e.g. "downloading", "pausedUP", "error"
	contentPath string
}

type fakeNZB struct {
	nzoID       string
	category    string
	inQueue     bool
	historyStat string // "Completed" or "Failed", only meaningful once !inQueue
	storage     string
	failMessage string
}

func newFakeServer(t *testing.T, apiKey string) *fakeServer {
	return &fakeServer{
		t:         t,
		apiKey:    apiKey,
		validSIDs: map[string]bool{},
		torrents:  map[string]*fakeTorrent{},
		nzbs:      map[string]*fakeNZB{},
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
	case r.URL.Path == "/api":
		f.handleSABnzbd(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f.loginCount++
	if r.FormValue("password") != f.apiKey {
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
	default:
		http.NotFound(w, r)
	}
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

func (f *fakeServer) handleSABnzbd(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20)
	if r.FormValue("apikey") != f.apiKey {
		json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "bad api key"})
		return
	}

	switch r.FormValue("mode") {
	case "addurl":
		f.nextNzo++
		id := "nzo-" + strconv.Itoa(f.nextNzo)
		f.nzbs[id] = &fakeNZB{nzoID: id, category: r.FormValue("cat"), inQueue: true}
		json.NewEncoder(w).Encode(map[string]any{"status": true, "nzo_ids": []string{id}})

	case "addfile":
		if r.MultipartForm == nil || len(r.MultipartForm.File["name"]) == 0 {
			json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "no file given"})
			return
		}
		f.nextNzo++
		id := "nzo-" + strconv.Itoa(f.nextNzo)
		f.nzbs[id] = &fakeNZB{nzoID: id, category: r.FormValue("cat"), inQueue: true}
		json.NewEncoder(w).Encode(map[string]any{"status": true, "nzo_ids": []string{id}})

	case "queue":
		type slot struct {
			NzoID string `json:"nzo_id"`
		}
		var slots []slot
		for _, n := range f.nzbs {
			if n.inQueue {
				slots = append(slots, slot{NzoID: n.nzoID})
			}
		}
		if slots == nil {
			slots = []slot{}
		}
		json.NewEncoder(w).Encode(map[string]any{"queue": map[string]any{"slots": slots}})

	case "history":
		type slot struct {
			NzoID       string `json:"nzo_id"`
			Status      string `json:"status"`
			Storage     string `json:"storage"`
			FailMessage string `json:"fail_message"`
		}
		var slots []slot
		for _, n := range f.nzbs {
			if !n.inQueue {
				slots = append(slots, slot{NzoID: n.nzoID, Status: n.historyStat, Storage: n.storage, FailMessage: n.failMessage})
			}
		}
		if slots == nil {
			slots = []slot{}
		}
		json.NewEncoder(w).Encode(map[string]any{"history": map[string]any{"slots": slots}})

	default:
		json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "unknown mode"})
	}
}

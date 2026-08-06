package sabnzbd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
)

// fakeServer is a minimal stand-in for a SABnzbd-API-compatible server
// (real SABnzbd, or AcerviNode's own compat shim) — just enough surface
// for this package's own client code to exercise against.
type fakeServer struct {
	apiKey string

	nzbs    map[string]*fakeNZB // by nzo_id
	nextNzo int
}

type fakeNZB struct {
	nzoID       string
	category    string
	inQueue     bool
	historyStat string // "Completed" or "Failed", only meaningful once !inQueue
	storage     string
	failMessage string
}

func newFakeServer(apiKey string) *fakeServer {
	return &fakeServer{
		apiKey: apiKey,
		nzbs:   map[string]*fakeNZB{},
	}
}

func (f *fakeServer) start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.handle))
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api" {
		http.NotFound(w, r)
		return
	}
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
		if r.FormValue("name") == "delete" {
			if n, ok := f.nzbs[r.FormValue("value")]; ok && n.inQueue {
				delete(f.nzbs, r.FormValue("value"))
			}
			json.NewEncoder(w).Encode(map[string]any{"status": true})
			return
		}
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
		if r.FormValue("name") == "delete" {
			if n, ok := f.nzbs[r.FormValue("value")]; ok && !n.inQueue {
				delete(f.nzbs, r.FormValue("value"))
			}
			json.NewEncoder(w).Encode(map[string]any{"status": true})
			return
		}
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

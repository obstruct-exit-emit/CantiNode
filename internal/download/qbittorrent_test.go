package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type qbitTorrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	Category string  `json:"category"`
}

// qbitStub simulates just enough of qBittorrent's Web API for Add/List, with
// realistic timing: torrents lists the items already present before any add;
// pendingAdd (if set) only appears in torrents/info once the add endpoint has
// actually been hit — so a test can tell "before this add" apart from "after
// it", the same distinction Add's own snapshotHashes/findHash rely on.
type qbitStub struct {
	torrents   []qbitTorrent
	pendingAdd *qbitTorrent
	added      bool
}

func (q *qbitStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/createCategory":
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/add":
			q.added = true
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			list := append([]qbitTorrent{}, q.torrents...)
			if q.added && q.pendingAdd != nil {
				list = append(list, *q.pendingAdd)
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestQBittorrent(t *testing.T, srv *httptest.Server) *qbittorrent {
	t.Helper()
	q := newQBittorrent(&ClientConfig{ID: 1, Name: "qBit", Type: TypeQBittorrent, Host: srv.URL, Category: "cantinode"})
	t.Cleanup(srv.Close)
	return q
}

// TestQBittorrentAddReturnsHash: the add endpoint itself never echoes a hash,
// but Add must still come back with one — looked up from the list right
// after — so the grab it's recorded under can be found again by id instead of
// only by a fragile title match. Regression for grabs permanently stuck
// "pending" after being removed from the queue, since a torrent grab's
// missing id meant removal could only resolve the grab by an exact title
// match.
func TestQBittorrentAddReturnsHash(t *testing.T) {
	stub := &qbitStub{
		pendingAdd: &qbitTorrent{Hash: "abc123", Name: "Dune Messiah", State: "downloading", Progress: 0.1, Category: "cantinode"},
	}
	q := newTestQBittorrent(t, stub.server())

	id, err := q.Add(context.Background(), "magnet:?xt=urn:btih:abc123", "Dune Messiah")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "abc123" {
		t.Errorf("Add returned id %q, want the real hash %q", id, "abc123")
	}
}

// TestQBittorrentAddFindsHashOnSubstringMatch: a tracker or the client itself
// can mutate the name away from an exact match (appending tags, stripping
// punctuation); an exact match is preferred but a substring match still
// resolves the hash rather than giving up and leaving the grab untraceable.
func TestQBittorrentAddFindsHashOnSubstringMatch(t *testing.T) {
	stub := &qbitStub{
		pendingAdd: &qbitTorrent{Hash: "def456", Name: "Dune Messiah [FL] {Narrator} 2023", State: "downloading", Category: "cantinode"},
	}
	q := newTestQBittorrent(t, stub.server())

	id, err := q.Add(context.Background(), "magnet:?xt=urn:btih:def456", "Dune Messiah")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "def456" {
		t.Errorf("Add returned id %q, want the fuzzily-matched hash %q", id, "def456")
	}
}

// TestQBittorrentAddIgnoresPreexistingSubstringMatch: an earlier series
// volume ("Dune Messiah") is already seeding in the same category when a
// shorter-titled release ("Dune") is added, and the new torrent doesn't show
// up under its own name within the lookup window. The substring fallback
// alone would match the PRE-EXISTING "Dune Messiah" (its normalized title
// contains "dune") and misattribute its hash to the new grab — corrupting
// both: the queue would link the wrong book to it, and cancelling one grab
// could resolve the other's. Add must return "" rather than guess, since the
// snapshot taken before this add already contained that torrent.
func TestQBittorrentAddIgnoresPreexistingSubstringMatch(t *testing.T) {
	stub := &qbitStub{
		torrents: []qbitTorrent{
			{Hash: "existing1", Name: "Dune Messiah", State: "downloading", Progress: 0.5, Category: "cantinode"},
		},
		// pendingAdd left nil: the new "Dune" torrent never becomes visible in
		// this test, simulating an add that landed but hasn't shown up yet.
	}
	q := newTestQBittorrent(t, stub.server())

	id, err := q.Add(context.Background(), "magnet:?xt=urn:btih:newone", "Dune")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "" {
		t.Errorf("Add returned id %q — misattributed the pre-existing \"Dune Messiah\" torrent's hash to this new \"Dune\" grab", id)
	}
}

// TestQBittorrentAddUsesMagnetHashDirectly: a debrid bridge (TorBox) can
// ignore the rename request entirely, always reporting the uploader's own
// torrent name instead — often a wildly different, or typo'd, string from
// the release title we grabbed under ("theq last emperox john scalzi" for
// "The Last Emperox"). Title matching can never bridge that gap, but the
// magnet's own info hash doesn't depend on it at all: Add must return that
// hash straight from the magnet URI, with no reliance on whatever title the
// client reports back. Regression for torrent imports becoming permanently
// unmatchable on a bridge that never honors rename.
func TestQBittorrentAddUsesMagnetHashDirectly(t *testing.T) {
	hash := "1234567890abcdef1234567890abcdef12345678" // 40 hex chars
	stub := &qbitStub{
		pendingAdd: &qbitTorrent{Hash: hash, Name: "theq last emperox john scalzi", State: "downloading", Category: "cantinode"},
	}
	q := newTestQBittorrent(t, stub.server())

	id, err := q.Add(context.Background(), "magnet:?xt=urn:btih:"+hash+"&dn=whatever", "The Last Emperox")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != hash {
		t.Errorf("Add returned id %q, want the magnet's own hash %q regardless of the client's reported title", id, hash)
	}
}

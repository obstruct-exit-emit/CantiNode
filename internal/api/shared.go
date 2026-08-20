package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/library"
)

const metadataTimeout = 60 * time.Second

// artistRefreshTimeout budgets handleMonitorMusicArtist/handleRefreshMusicArtist,
// whose synchronous path includes BrowseArtistReleaseGroups — now fully
// paginated (see internal/musicbrainz's browseMaxPages) rather than
// truncated at 25, so an extremely prolific artist (well into the
// thousands of release groups) can need many sequential requests at
// MusicBrainz's ~1.1s-per-request rate limit. metadataTimeout's 60s was
// sized for the single-request lookups the rest of this package makes and
// leaves too little headroom here.
const artistRefreshTimeout = 5 * time.Minute

// metadataCtx and artistRefreshCtx deliberately take no *http.Request and
// do NOT derive from a handler's own r.Context() — found live: a client
// disconnect (a page refresh, most commonly) cancels that context
// immediately, and Go's net/http propagates the cancellation into any
// request already in flight. Approving a match (or a batch of them — the
// unmatched-files page's "Approve all" fires every suggestion's request
// in parallel) can legitimately take a while per request: each one's
// MusicBrainz lookup queues behind the shared ~1.1s-per-request rate
// limiter, so a batch of even a dozen files takes many seconds
// server-side. A user refreshing partway through — an easy, unremarkable
// thing to do while waiting — canceled every still-queued match's context
// and killed it, leaving those files silently unmatched with no error
// surfaced anywhere (the browser's own fetch was already gone, so nothing
// was left to show one to). Once the server has accepted a request enough
// to start doing real work, that work should finish (or hit its own
// timeout) regardless of whether the browser that asked for it is still
// around to see the response — the same reasoning handleTriggerMusicScan's
// own goroutine already uses context.Background() for. No *http.Request
// parameter, rather than one that's silently unused, so a future change
// can't accidentally reintroduce r.Context() here without it being an
// obvious signature change.
func (s *server) metadataCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), metadataTimeout)
}

func (s *server) artistRefreshCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), artistRefreshTimeout)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// writeStoreError maps internal/library store errors to HTTP responses —
// used by the generic root-folder/quality-profile endpoints music also
// relies on.
func writeStoreError(w http.ResponseWriter, err error) {
	if err == library.ErrNotFound {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// handleImage proxies (and caches) a remote cover/portrait URL so the
// browser never talks to arbitrary third-party hosts directly — shared by
// every media type, including music's artist photos from TheAudioDB.
func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	data, contentType, err := s.images.Fetch(ctx, url)
	if err != nil {
		// Fall back to the origin only for real web URLs — never reflect an
		// arbitrary scheme into a Location header.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			writeError(w, http.StatusBadRequest, "url must be http(s)")
			return
		}
		slog.Debug("image proxy fetch failed, redirecting to origin", "url", url, "error", err)
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Write(data)
}

// handleClearAllCache empties the cached-image proxy store.
func (s *server) handleClearAllCache(w http.ResponseWriter, r *http.Request) {
	removed, freed, err := s.images.Clear()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":    removed,
		"freedBytes": freed,
	})
}

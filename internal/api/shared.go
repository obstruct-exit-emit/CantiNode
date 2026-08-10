package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/library"
)

const metadataTimeout = 60 * time.Second

func (s *server) metadataCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), metadataTimeout)
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

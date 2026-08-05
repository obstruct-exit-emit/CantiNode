package api

import "net/http"

// handleMusicBrainzSearch proxies a fuzzy recording search to MusicBrainz
// — used by the manual-review UI to let a human search for the right
// recording when an automatic match wasn't confident enough (or wrong).
// Goes through the scanner's own rate-limited client rather than a
// separate one, so a burst of manual searches shares the same 1 req/sec
// budget as scanning instead of doubling MusicBrainz load.
func (s *Server) handleMusicBrainzSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	results, err := s.scanner.SearchMusicBrainz(r.Context(), q.Get("artist"), q.Get("album"), q.Get("title"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, results)
}

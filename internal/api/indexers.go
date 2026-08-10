package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/indexer"
)

const indexerTestTimeout = 30 * time.Second

func writeIndexerError(w http.ResponseWriter, err error) {
	if errors.Is(err, indexer.ErrNotFound) {
		writeError(w, http.StatusNotFound, "indexer not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// decodeIndexer reads and validates an indexer definition from the body.
// Two dialects arrive here: LibriNode's native flat JSON and the Readarr v1
// resource Prowlarr pushes (marked by an "implementation" key with fields[]).
func decodeIndexer(r *http.Request) (*indexer.Indexer, string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, "reading body"
	}

	var probe struct {
		Implementation string `json:"implementation"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Implementation != "" {
		var res arrIndexerResource
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, "invalid JSON body"
		}
		in, err := res.toIndexer()
		if err != nil {
			return nil, err.Error()
		}
		if in.Name == "" {
			return nil, "name is required"
		}
		return in, ""
	}

	var in indexer.Indexer
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, "invalid JSON body"
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Name == "" {
		return nil, "name is required"
	}
	if in.Priority <= 0 || in.Priority > 50 {
		in.Priority = 25
	}

	// A native source: no Newznab/Torznab URL or categories — the built-in
	// implementation owns all of that. The base-URL field optionally carries a
	// primary site URL plus comma-separated fallbacks (rotating-domain sites
	// run mirrors); each entry must be a real http(s) URL. The API key passes
	// through untouched.
	if def, ok := indexer.NativeDefFor(in.Type); ok {
		cleaned := []string{}
		for _, part := range strings.Split(in.BaseURL, ",") {
			p := strings.TrimRight(strings.TrimSpace(part), "/")
			if p == "" {
				continue
			}
			if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
				return nil, "every site URL must be an http(s) URL"
			}
			cleaned = append(cleaned, p)
		}
		in.BaseURL = strings.Join(cleaned, ",")
		if def.NeedsAPIKey && in.APIKey == "" {
			return nil, def.DisplayName + " needs an API key"
		}
		return &in, ""
	}

	if in.Type != indexer.TypeNewznab && in.Type != indexer.TypeTorznab {
		return nil, "type must be newznab, torznab, or a native source"
	}
	if !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return nil, "baseUrl must be an http(s) URL"
	}
	if in.AudioCategories == "" {
		in.AudioCategories = "3030"
	}
	return &in, ""
}

// handleListNativeIndexers lists the built-in native indexer implementations
// the Settings UI can offer as indexer types (name, label, protocol, the media
// types each serves, and whether it takes an optional base URL or an API key).
func (s *server) handleListNativeIndexers(w http.ResponseWriter, r *http.Request) {
	defs := indexer.NativeImplementations()
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{
			"name":           d.Name,
			"displayName":    d.DisplayName,
			"protocol":       d.Protocol,
			"mediaTypes":     d.MediaTypes,
			"defaultBaseUrl": d.DefaultBaseURL,
			"needsApiKey":    d.NeedsAPIKey,
			"wip":            d.WIP,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleListIndexers(w http.ResponseWriter, r *http.Request) {
	indexers, err := s.indexers.Store().List()
	if err != nil {
		writeIndexerError(w, err)
		return
	}
	// Native sources are LibriNode-managed only: hide them from Prowlarr so it
	// never treats them as indexers it owns (and prunes them on sync). The
	// app's own UI (any non-Prowlarr caller) still sees them.
	prowlarr := isProwlarr(r)
	resources := make([]map[string]any, 0, len(indexers))
	for i := range indexers {
		if prowlarr && indexer.IsNativeType(indexers[i].Type) {
			continue
		}
		resources = append(resources, mergedIndexerResource(&indexers[i]))
	}
	writeJSON(w, http.StatusOK, resources)
}

func (s *server) handleGetIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ind, err := s.indexers.Store().Get(id)
	if err != nil {
		writeIndexerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mergedIndexerResource(ind))
}

func (s *server) handleAddIndexer(w http.ResponseWriter, r *http.Request) {
	in, msg := decodeIndexer(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.indexers.Store().Add(in); err != nil {
		writeError(w, http.StatusConflict, "could not save indexer (duplicate name?): "+err.Error())
		return
	}
	s.refreshHealth()
	writeJSON(w, http.StatusCreated, mergedIndexerResource(in))
}

func (s *server) handleUpdateIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	in, msg := decodeIndexer(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	in.ID = id
	if err := s.indexers.Store().Update(in); err != nil {
		writeIndexerError(w, err)
		return
	}
	updated, err := s.indexers.Store().Get(id)
	if err != nil {
		writeIndexerError(w, err)
		return
	}
	s.refreshHealth()
	writeJSON(w, http.StatusOK, mergedIndexerResource(updated))
}

func (s *server) handleDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.indexers.Store().Delete(id); err != nil {
		writeIndexerError(w, err)
		return
	}
	s.refreshHealth()
	w.WriteHeader(http.StatusNoContent)
}

// handleTestIndexer checks an unsaved indexer definition against the live
// endpoint (fetches its capabilities).
func (s *server) handleTestIndexer(w http.ResponseWriter, r *http.Request) {
	in, msg := decodeIndexer(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), indexerTestTimeout)
	defer cancel()

	if err := s.indexers.Test(ctx, in); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

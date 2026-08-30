package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/importlist"
)

const importListTestTimeout = 30 * time.Second

func writeImportListError(w http.ResponseWriter, err error) {
	if errors.Is(err, importlist.ErrNotFound) {
		writeError(w, http.StatusNotFound, "import list not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// decodeImportList reads and validates an import list definition from the
// body — each type has its own required field, the same per-type shape
// decodeIndexer uses for a native source's own requirements.
func decodeImportList(r *http.Request) (*importlist.ImportList, string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, "reading body"
	}

	var in importlist.ImportList
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, "invalid JSON body"
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, "name is required"
	}

	switch in.Type {
	case importlist.TypeMusicBrainzSeries:
		in.SeriesMBID = strings.TrimSpace(in.SeriesMBID)
		if in.SeriesMBID == "" {
			return nil, "a MusicBrainz series MBID is required"
		}
	case importlist.TypeList:
		in.SourceURL = strings.TrimSpace(in.SourceURL)
		if strings.TrimSpace(in.ListText) == "" && in.SourceURL == "" {
			return nil, "pasted list text or a source URL is required"
		}
		if in.SourceURL != "" && !strings.HasPrefix(in.SourceURL, "http://") && !strings.HasPrefix(in.SourceURL, "https://") {
			return nil, "source URL must be an http(s) URL"
		}
	case importlist.TypeLastFM:
		in.LastfmTarget = strings.TrimSpace(in.LastfmTarget)
		if in.LastfmTarget == "" {
			return nil, "a Last.fm username or tag is required"
		}
		if in.LastfmKind != importlist.LastfmKindTag {
			in.LastfmKind = importlist.LastfmKindUser
		}
	default:
		return nil, "type must be musicbrainz_series, list, or lastfm"
	}

	return &in, ""
}

func (s *server) handleListImportLists(w http.ResponseWriter, r *http.Request) {
	lists, err := s.importLists.Store().List()
	if err != nil {
		writeImportListError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lists)
}

func (s *server) handleAddImportList(w http.ResponseWriter, r *http.Request) {
	in, msg := decodeImportList(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.importLists.Store().Add(in); err != nil {
		writeError(w, http.StatusConflict, "could not save import list (duplicate name?): "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *server) handleUpdateImportList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	in, msg := decodeImportList(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	in.ID = id
	if err := s.importLists.Store().Update(in); err != nil {
		writeImportListError(w, err)
		return
	}
	updated, err := s.importLists.Store().Get(id)
	if err != nil {
		writeImportListError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleDeleteImportList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.importLists.Store().Delete(id); err != nil {
		writeImportListError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestImportList resolves an unsaved import list definition the same
// way a real sync would, without adding anything — the Settings "test"
// button's own confirmation that a series/list/Last.fm source is reachable
// and actually resolves to at least one artist.
func (s *server) handleTestImportList(w http.ResponseWriter, r *http.Request) {
	in, msg := decodeImportList(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), importListTestTimeout)
	defer cancel()

	mbids, err := s.importLists.Resolve(ctx, *in)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resolvedCount": len(mbids)})
}

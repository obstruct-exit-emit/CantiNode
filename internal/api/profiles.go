package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/librinode/librinode/internal/library"
)

func (s *server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Prowlarr reads quality profiles as Readarr resources during app sync;
	// serve it the Readarr-shaped view (the browser UI keeps its native
	// shape — notably cutoff stays a format string, not a Readarr quality id).
	if isProwlarr(r) {
		out := make([]map[string]any, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, readarrQualityProfile(p))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *server) handleAddProfile(w http.ResponseWriter, r *http.Request) {
	var p library.QualityProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p.IsDefault = false // defaults are assigned via the default endpoint
	if err := s.store.AddProfile(&p); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "UNIQUE") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p library.QualityProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p.ID = id
	if err := s.store.UpdateProfile(&p); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			writeError(w, http.StatusNotFound, "profile not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.store.GetProfile(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleDefaultProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.SetDefaultProfile(id); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := s.store.GetProfile(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.DeleteProfile(id); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			writeError(w, http.StatusNotFound, "profile not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

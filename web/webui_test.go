package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerServesSomethingAtRoot(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandlerFallsBackToIndexForUnknownPath(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	rootRec := httptest.NewRecorder()
	h.ServeHTTP(rootRec, httptest.NewRequest(http.MethodGet, "/", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/client/side/route", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if rec.Body.String() != rootRec.Body.String() {
		t.Error("unknown path should fall back to the same content as /")
	}
}

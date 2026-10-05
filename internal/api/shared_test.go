package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestImageProxyFailureDoesNotRedirect is the regression test for an open
// redirect: handleImage used to fall back to
// http.Redirect(w, r, url, http.StatusFound) whenever images.Fetch failed
// for any reason (a non-200, a timeout, or the response simply not being
// image content) — since that fallback fired for any http(s) url
// regardless of why the fetch failed, an authenticated caller could pass
// GET /api/v1/image?url=https://evil.example/anything and have CantiNode's
// own origin send their browser a 302 straight to it. That also defeated
// this handler's entire stated purpose ("the browser never talks to
// arbitrary third-party hosts directly") for exactly the case — a failure
// — where the guarantee mattered most. A failed fetch must now be a plain
// error, never a redirect.
func TestImageProxyFailureDoesNotRedirect(t *testing.T) {
	notAnImage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer notAnImage.Close()

	a := newTestAPI(t)
	req, err := http.NewRequest("GET", a.srv.URL+"/api/v1/image?url="+notAnImage.URL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)

	// A client that never follows a redirect on its own — if the handler
	// ever regresses to redirecting, this must see the 3xx itself rather
	// than silently following it to wherever it points.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		t.Fatalf("status = %d with Location %q — image proxy failure must never redirect the caller", resp.StatusCode, resp.Header.Get("Location"))
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Errorf("unexpected Location header on failure: %q", loc)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

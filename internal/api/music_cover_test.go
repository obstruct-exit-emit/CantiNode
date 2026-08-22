package api

import (
	"fmt"
	"net/http"
	"testing"
)

// TestRetryMusicAlbumCoverReturns404ForUnknownAlbum confirms the retry
// endpoint's own id validation runs before ever touching coverart.Client —
// the coverart.Client.Refetch mechanics themselves (bypassing both
// sentinels, returning ErrNoCoverArt when a fresh check still finds
// nothing) are covered directly in internal/coverart's own test suite,
// which can point at a fake TheAudioDB/Cover Art Archive server; this
// package's router wires the real ones, with no override seam for a test
// to use instead.
func TestRetryMusicAlbumCoverReturns404ForUnknownAlbum(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", fmt.Sprintf("/api/v1/music/album/%d/cover/retry", 999999), nil, nil), http.StatusNotFound)
}

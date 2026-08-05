package acervinode

import "errors"

// ErrNotFound means AcerviNode no longer recognizes a download CantiNode
// is tracking — deleted directly through AcerviNode's own UI, most
// likely.
var ErrNotFound = errors.New("acervinode: download not found")

// State is Status's simplified three-state view of a download —
// collapsing every qBittorrent/SABnzbd-shim sub-state (queued,
// downloading, verifying, moving, ...) down to what internal/acquisition
// actually needs to act on.
type State string

const (
	StateDownloading State = "downloading"
	StateCompleted   State = "completed"
	StateError       State = "error"
)

// Status is a download's current state as AcerviNode reports it.
type Status struct {
	State State
	// LocalPath is where the download's files live on AcerviNode's own
	// local disk — only set once State == StateCompleted. CantiNode reads
	// from this path directly to import (see internal/acquisition), which
	// requires CantiNode and AcerviNode to share a filesystem view — the
	// same assumption Sonarr/Radarr already make about any download
	// client they use.
	LocalPath string
	// ErrorMessage is set only when State == StateError.
	ErrorMessage string
}

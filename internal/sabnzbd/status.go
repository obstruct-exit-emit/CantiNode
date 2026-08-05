package sabnzbd

import "errors"

// ErrNotFound means the server no longer recognizes an nzb CantiNode is
// tracking — deleted directly through its own UI, most likely.
var ErrNotFound = errors.New("sabnzbd: download not found")

// State is Status's simplified three-state view of a download —
// collapsing every real-SABnzbd sub-status (Queued, Downloading,
// Verifying, Repairing, Extracting, Moving, ...) down to what
// internal/acquisition actually needs to act on.
type State string

const (
	StateDownloading State = "downloading"
	StateCompleted   State = "completed"
	StateError       State = "error"
)

// Status is a download's current state as the server reports it.
type Status struct {
	State State
	// LocalPath is where the download's files live on the server's own
	// local disk — only set once State == StateCompleted. Reading from it
	// directly requires CantiNode and the download client to share a
	// filesystem view — the same assumption any *arr app's download
	// client integration already makes.
	LocalPath string
	// ErrorMessage is set only when State == StateError.
	ErrorMessage string
}

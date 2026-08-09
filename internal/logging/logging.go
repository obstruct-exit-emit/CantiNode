// Package logging provides the on-disk log file: a size-rotated writer that
// slog tees into alongside stdout. The UI's log viewer reads the current
// file back through the API.
package logging

import (
	"fmt"
	"os"
	"sync"
)

// RotatingFile appends to path and rotates by size: librinode.log becomes
// librinode.log.1 (older files shift up), keeping `keep` rotated files.
type RotatingFile struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	f       *os.File
	size    int64
}

// NewRotatingFile opens (or creates) the log file for appending.
func NewRotatingFile(path string, maxSize int64, keep int) (*RotatingFile, error) {
	r := &RotatingFile{path: path, maxSize: maxSize, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f = f
	r.size = info.Size()
	return nil
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			// Rotation failing must not lose the log line; keep appending.
			fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", err)
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts librinode.log.(n) → .(n+1), the live file → .1, and reopens.
func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	for i := r.keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		// Reopen even if the rename failed so logging keeps working.
		r.open()
		return err
	}
	return r.open()
}

// Close flushes and closes the underlying file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

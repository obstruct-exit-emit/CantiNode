// Package acquisition is CantiNode's optional Lidarr-parity layer:
// monitor an artist, want their albums, search Prowlarr for releases,
// grab one (manual only in v1 — no auto-grab, see ROADMAP.md) via
// AcerviNode, and import the finished download into the library once
// AcerviNode reports it done.
//
// Prowlarr and AcerviNode are both optional — a fresh CantiNode install
// has neither configured, and every method here reports a plain error
// (not a panic) rather than assuming they're set. See Service.
// UpdateClients for how they're wired in once Settings has real
// connection details.
package acquisition

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/cantinode/cantinode/internal/acervinode"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/scanner"
)

// Service ties MusicBrainz, Prowlarr, AcerviNode, the database, and
// internal/scanner together into the monitor -> want -> search -> grab
// -> import pipeline.
type Service struct {
	db      *database.DB
	mb      *musicbrainz.Client
	scanner *scanner.Scanner
	logger  *slog.Logger

	clientsMu sync.RWMutex
	prowlarr  *prowlarr.Client   // nil until Settings configures a Prowlarr URL/key
	acervi    *acervinode.Client // nil until Settings configures an AcerviNode URL/key
}

// New returns a Service with no Prowlarr/AcerviNode client configured
// yet — see UpdateClients.
func New(db *database.DB, mb *musicbrainz.Client, sc *scanner.Scanner, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, mb: mb, scanner: sc, logger: logger}
}

// UpdateClients swaps in new Prowlarr/AcerviNode clients — called
// whenever Settings saves new connection details for either, so a
// changed URL/key takes effect on the very next search/grab/poll, no
// restart needed (same pattern as scanner.Scanner.UpdateSettings). Pass
// nil for either to clear it back to "not configured".
func (s *Service) UpdateClients(pw *prowlarr.Client, av *acervinode.Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.prowlarr = pw
	s.acervi = av
}

func (s *Service) getProwlarr() *prowlarr.Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.prowlarr
}

func (s *Service) getAcervi() *acervinode.Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.acervi
}

var errProwlarrNotConfigured = fmt.Errorf("acquisition: Prowlarr is not configured (set its URL/API key in Settings)")
var errAcerviNotConfigured = fmt.Errorf("acquisition: AcerviNode is not configured (set its URL/API key in Settings)")

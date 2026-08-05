// Package acquisition is CantiNode's optional Lidarr-parity layer:
// monitor an artist, want their albums, search Prowlarr for releases,
// grab one (manual only in v1 — no auto-grab, see ROADMAP.md) via a
// qBittorrent- or SABnzbd-API-compatible download client, and import the
// finished download into the library once that client reports it done.
//
// Prowlarr and the download clients are all optional — a fresh CantiNode
// install has none configured, and every method here reports a plain
// error (not a panic) rather than assuming they're set. See Service.
// UpdateClients for how they're wired in once Settings has real
// connection details.
package acquisition

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/qbittorrent"
	"github.com/cantinode/cantinode/internal/sabnzbd"
	"github.com/cantinode/cantinode/internal/scanner"
)

// Service ties MusicBrainz, Prowlarr, a qBittorrent-API-compatible
// torrent client, a SABnzbd-API-compatible usenet client, the database,
// and internal/scanner together into the monitor -> want -> search ->
// grab -> import pipeline.
//
// The two download clients are independent and separately optional —
// each can point at a genuine standalone qBittorrent/SABnzbd instance, or
// at AcerviNode (which happens to expose both compat shims on one host),
// or at nothing at all.
type Service struct {
	db      *database.DB
	mb      *musicbrainz.Client
	scanner *scanner.Scanner
	logger  *slog.Logger

	clientsMu sync.RWMutex
	prowlarr  *prowlarr.Client    // nil until Settings configures a Prowlarr URL/key
	qbit      *qbittorrent.Client // nil until Settings configures a qBittorrent-compatible URL/credentials
	sab       *sabnzbd.Client     // nil until Settings configures a SABnzbd-compatible URL/API key
}

// New returns a Service with no Prowlarr/download-client configured yet —
// see UpdateClients.
func New(db *database.DB, mb *musicbrainz.Client, sc *scanner.Scanner, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, mb: mb, scanner: sc, logger: logger}
}

// UpdateClients swaps in new Prowlarr/qBittorrent/SABnzbd clients —
// called whenever Settings saves new connection details for any of them,
// so a changed URL/key takes effect on the very next search/grab/poll, no
// restart needed (same pattern as scanner.Scanner.UpdateSettings). Pass
// nil for any of them to clear it back to "not configured".
func (s *Service) UpdateClients(pw *prowlarr.Client, qbit *qbittorrent.Client, sab *sabnzbd.Client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.prowlarr = pw
	s.qbit = qbit
	s.sab = sab
}

func (s *Service) getProwlarr() *prowlarr.Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.prowlarr
}

func (s *Service) getQBittorrent() *qbittorrent.Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.qbit
}

func (s *Service) getSABnzbd() *sabnzbd.Client {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.sab
}

var errProwlarrNotConfigured = fmt.Errorf("acquisition: Prowlarr is not configured (set its URL/API key in Settings)")
var errQBittorrentNotConfigured = fmt.Errorf("acquisition: qBittorrent is not configured (set its URL/username/password in Settings)")
var errSABnzbdNotConfigured = fmt.Errorf("acquisition: SABnzbd is not configured (set its URL/API key in Settings)")

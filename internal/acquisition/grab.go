package acquisition

import (
	"context"
	"fmt"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/prowlarr"
)

// GrabRelease sends rel (a result from a prior SearchReleases call — the
// caller, internal/api, passes back whichever one the user picked) to
// whichever download client matches its protocol and records a Download
// tracking it. No automatic decision-making happens here: which release
// to grab is always a human choice (see ROADMAP.md's v1 scoping — no
// auto-grab).
//
// The target root folder is always the first one (ordered by path) —
// v1 doesn't yet expose a per-artist or per-grab destination choice;
// see ROADMAP.md.
func (s *Service) GrabRelease(ctx context.Context, wantedAlbumID int64, rel prowlarr.Release) (*database.Download, error) {
	pw := s.getProwlarr()
	if pw == nil {
		return nil, errProwlarrNotConfigured
	}

	if _, err := s.db.GetWantedAlbum(ctx, wantedAlbumID); err != nil {
		return nil, fmt.Errorf("get wanted album: %w", err)
	}
	folders, err := s.db.ListRootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	if len(folders) == 0 {
		return nil, fmt.Errorf("acquisition: no root folders configured — add one before grabbing a release")
	}
	rootFolder := folders[0]

	content, err := pw.FetchContent(ctx, rel)
	if err != nil {
		return nil, fmt.Errorf("fetch release content: %w", err)
	}

	protocol, clientID, err := s.sendToDownloadClient(ctx, rel, content)
	if err != nil {
		return nil, fmt.Errorf("send to download client: %w", err)
	}

	download, err := s.db.CreateDownload(ctx, wantedAlbumID, rootFolder.ID, protocol, clientID, rel.Title, rel.Indexer)
	if err != nil {
		return nil, fmt.Errorf("create download: %w", err)
	}
	if err := s.db.SetWantedAlbumStatus(ctx, wantedAlbumID, database.WantedStatusDownloading); err != nil {
		return download, fmt.Errorf("set wanted album downloading: %w", err)
	}
	return download, nil
}

// sendToDownloadClient hands content to whichever configured client
// matches it: a resolved magnet URI always means torrent, so qBittorrent
// (that's the one unambiguous case — see prowlarr.KindMagnet), while
// actual downloaded file bytes need rel.Protocol to know whether they're
// a .torrent (qBittorrent) or a .nzb (SABnzbd).
func (s *Service) sendToDownloadClient(ctx context.Context, rel prowlarr.Release, content *prowlarr.FetchedContent) (database.DownloadProtocol, string, error) {
	if content.Kind == prowlarr.KindMagnet {
		qbit := s.getQBittorrent()
		if qbit == nil {
			return "", "", errQBittorrentNotConfigured
		}
		hash, err := qbit.AddMagnet(ctx, content.MagnetURI)
		if err != nil {
			return "", "", err
		}
		return database.ProtocolTorrent, hash, nil
	}

	switch rel.Protocol {
	case prowlarr.ProtocolTorrent:
		qbit := s.getQBittorrent()
		if qbit == nil {
			return "", "", errQBittorrentNotConfigured
		}
		hash, err := qbit.AddTorrentFile(ctx, content.Filename, content.Data)
		if err != nil {
			return "", "", err
		}
		return database.ProtocolTorrent, hash, nil
	case prowlarr.ProtocolUsenet:
		sab := s.getSABnzbd()
		if sab == nil {
			return "", "", errSABnzbdNotConfigured
		}
		nzoID, err := sab.AddByFile(ctx, content.Filename, content.Data, rel.Title)
		if err != nil {
			return "", "", err
		}
		return database.ProtocolUsenet, nzoID, nil
	default:
		return "", "", fmt.Errorf("acquisition: release %q has downloaded content but an unknown protocol — can't tell if it's a torrent or usenet", rel.Title)
	}
}

package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// AddMagnet adds magnetURI and returns its infohash (parsed from the
// magnet URI itself — the add endpoint's response is just "Ok."/"Fails.",
// no ID), the identifier GetStatus polls on.
func (c *Client) AddMagnet(ctx context.Context, magnetURI string) (infoHash string, err error) {
	hash, err := magnetInfoHash(magnetURI)
	if err != nil {
		return "", err
	}

	form := url.Values{"urls": {magnetURI}, "category": {musicCategory}}
	resp, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/add", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "Ok." {
		return "", fmt.Errorf("qbittorrent: add magnet failed: status %d: %s", resp.StatusCode, body)
	}
	return hash, nil
}

// AddTorrentFile uploads a .torrent file's raw bytes via the multipart
// add path. Unlike AddMagnet, the resulting infohash isn't known up
// front — computing it would mean bencode-parsing the torrent's own info
// dict, so instead this takes a snapshot of the music category's known
// hashes before adding and returns whichever hash is new afterward. Only
// reliable for one add at a time (true of every current caller —
// internal/acquisition grabs one release at a time, on an explicit user
// action).
func (c *Client) AddTorrentFile(ctx context.Context, filename string, data []byte) (infoHash string, err error) {
	before, err := c.listCategoryHashes(ctx)
	if err != nil {
		return "", fmt.Errorf("snapshot existing torrents: %w", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("category", musicCategory); err != nil {
		return "", err
	}
	part, err := mw.CreateFormFile("torrents", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	resp, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/add", &body, mw.FormDataContentType())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(respBody)) != "Ok." {
		return "", fmt.Errorf("qbittorrent: add torrent file failed: status %d: %s", resp.StatusCode, respBody)
	}

	after, err := c.listCategoryHashes(ctx)
	if err != nil {
		return "", fmt.Errorf("list torrents after add: %w", err)
	}
	for h := range after {
		if !before[h] {
			return h, nil
		}
	}
	return "", fmt.Errorf("qbittorrent: torrent file added but no new hash appeared in category %q", musicCategory)
}

func (c *Client) listCategoryHashes(ctx context.Context) (map[string]bool, error) {
	items, err := c.listCategoryTorrents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[it.Hash] = true
	}
	return out, nil
}

type torrentInfo struct {
	Hash        string  `json:"hash"`
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"`
	Progress    float64 `json:"progress"`
}

func (c *Client) listCategoryTorrents(ctx context.Context) ([]torrentInfo, error) {
	q := url.Values{"category": {musicCategory}}
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qbittorrent: list torrents: status %d: %s", resp.StatusCode, body)
	}
	var items []torrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode torrents/info response: %w", err)
	}
	return items, nil
}

// Remove deletes infoHash from the server, including its downloaded
// files — used to cancel a grab CantiNode itself initiated. Not an error
// if the server no longer knows this hash (already removed some other
// way); real qBittorrent's own delete endpoint doesn't distinguish that
// case from success either.
func (c *Client) Remove(ctx context.Context, infoHash string) error {
	form := url.Values{"hashes": {infoHash}, "deleteFiles": {"true"}}
	resp, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/delete", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qbittorrent: remove torrent: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// GetStatus polls the server for infoHash's current status. Returns
// ErrNotFound if it no longer knows this hash at all.
func (c *Client) GetStatus(ctx context.Context, infoHash string) (*Status, error) {
	q := url.Values{"hashes": {infoHash}}
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qbittorrent: get torrent status: status %d: %s", resp.StatusCode, body)
	}
	var items []torrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode torrents/info response: %w", err)
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return torrentInfoToStatus(items[0]), nil
}

// torrentInfoToStatus maps real qBittorrent's own state vocabulary onto
// Status.State. pausedUP/stoppedUP means fully downloaded and no longer
// actively transferring — real qBittorrent's own "done" signal (matches
// AcerviNode's own compat-shim convention too, since it deliberately
// mirrors this exact vocabulary — see docs/qbittorrent-api.md there).
func torrentInfoToStatus(t torrentInfo) *Status {
	switch t.State {
	case "pausedUP", "stoppedUP":
		return &Status{State: StateCompleted, LocalPath: t.ContentPath}
	case "error", "missingFiles":
		return &Status{State: StateError, ErrorMessage: "reported as errored (state: " + t.State + ")"}
	default:
		return &Status{State: StateDownloading}
	}
}

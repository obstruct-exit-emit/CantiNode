package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/redact"
)

// SABnzbd's API is a single endpoint: GET /api?mode=...&apikey=...&output=json.
// Queue and history are separate calls; LibriNode merges them into one view.
type sabnzbd struct {
	cfg   *ClientConfig
	httpc *http.Client
}

func newSABnzbd(cfg *ClientConfig) *sabnzbd {
	return &sabnzbd{cfg: cfg, httpc: &http.Client{Timeout: 30 * time.Second}}
}

func (s *sabnzbd) api(ctx context.Context, params url.Values, out any) error {
	params.Set("apikey", s.cfg.APIKey)
	params.Set("output", "json")
	endpoint := strings.TrimRight(s.cfg.Host, "/") + "/api?" + params.Encode()
	// Captured before the request so a failure (which embeds the URL
	// verbatim) or an echoing error body can be scrubbed of the API key.
	secrets := redact.Values(endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("sabnzbd: %w", redact.URLError(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("sabnzbd: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sabnzbd: HTTP %d: %.100s", resp.StatusCode, redact.Text(string(body), secrets))
	}
	// Errors arrive as {"status": false, "error": "..."} regardless of mode.
	var apiErr struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil &&
		apiErr.Status != nil && !*apiErr.Status && apiErr.Error != "" {
		return fmt.Errorf("sabnzbd: %s", apiErr.Error)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("sabnzbd: decoding response: %w", err)
		}
	}
	return nil
}

func (s *sabnzbd) Test(ctx context.Context) error {
	var out struct {
		Version string `json:"version"`
	}
	if err := s.api(ctx, url.Values{"mode": {"version"}}, &out); err != nil {
		return err
	}
	if out.Version == "" {
		return fmt.Errorf("sabnzbd: no version in response — wrong endpoint?")
	}
	// version works without auth on some setups; queue verifies the key.
	return s.api(ctx, url.Values{"mode": {"queue"}, "limit": {"1"}}, nil)
}

// Add sends a release to SABnzbd. It first fetches the NZB itself and uploads
// the file content (addfile): LibriNode can reach the indexer/proxy on the
// LAN, but the download client often can't (SABnzbd behind NAT, or a
// Real-Debrid usenet bridge whose cloud side can't fetch a LAN URL) — handing
// it a URL leaves grabs stuck at 0 bytes. Uploading the content sidesteps
// that and names the job properly. If the fetch fails (unreachable, not an
// NZB), it falls back to handing SABnzbd the URL (addurl).
func (s *sabnzbd) Add(ctx context.Context, dlURL, title string) (string, error) {
	if nzb, err := s.fetchNZB(ctx, dlURL); err == nil {
		if id, err := s.addFile(ctx, nzb, title); err == nil {
			return id, nil
		}
	}
	return s.addURL(ctx, dlURL, title)
}

func (s *sabnzbd) addURL(ctx context.Context, dlURL, title string) (string, error) {
	var out struct {
		Status bool     `json:"status"`
		NzoIDs []string `json:"nzo_ids"`
	}
	params := url.Values{
		"mode":    {"addurl"},
		"name":    {dlURL},
		"nzbname": {title},
		"cat":     {s.cfg.Category},
	}
	if err := s.api(ctx, params, &out); err != nil {
		return "", err
	}
	if !out.Status || len(out.NzoIDs) == 0 {
		return "", fmt.Errorf("sabnzbd did not accept the NZB")
	}
	return out.NzoIDs[0], nil
}

// fetchNZB downloads the NZB from the release URL (following the indexer's
// redirect to the actual NZB). It rejects non-NZB bodies — HTML error pages,
// a magnet/torrent redirect — so those fall back to addurl instead of being
// uploaded as junk.
func (s *sabnzbd) fetchNZB(ctx context.Context, dlURL string) ([]byte, error) {
	if !strings.HasPrefix(dlURL, "http://") && !strings.HasPrefix(dlURL, "https://") {
		return nil, fmt.Errorf("not an http url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		// dlURL is the release's download URL — Newznab convention embeds the
		// indexer's own apikey in it, so a failure here must not carry that
		// key into logs or the UI.
		return nil, redact.URLError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching nzb: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, err
	}
	head := bytes.ToLower(bytes.TrimSpace(body))
	if len(head) > 512 {
		head = head[:512]
	}
	if !bytes.Contains(head, []byte("<nzb")) && !bytes.HasPrefix(head, []byte("<?xml")) {
		return nil, fmt.Errorf("response is not an nzb")
	}
	return body, nil
}

// addFile uploads NZB content to SABnzbd (mode=addfile, multipart). nzbname
// gives the job a readable name regardless of the URL it came from.
func (s *sabnzbd) addFile(ctx context.Context, nzb []byte, title string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("name", nzbFilename(title))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(nzb); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	params := url.Values{
		"mode":    {"addfile"},
		"nzbname": {title},
		"cat":     {s.cfg.Category},
		"apikey":  {s.cfg.APIKey},
		"output":  {"json"},
	}
	endpoint := strings.TrimRight(s.cfg.Host, "/") + "/api?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := s.httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sabnzbd: HTTP %d: %.100s", resp.StatusCode, respBody)
	}
	var out struct {
		Status bool     `json:"status"`
		NzoIDs []string `json:"nzo_ids"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("sabnzbd: decoding response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("sabnzbd: %s", out.Error)
	}
	if !out.Status || len(out.NzoIDs) == 0 {
		return "", fmt.Errorf("sabnzbd did not accept the NZB")
	}
	return out.NzoIDs[0], nil
}

// nzbFilename makes a filesystem-safe "<title>.nzb" for the upload.
func nzbFilename(title string) string {
	safe := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r':
			return '_'
		}
		return r
	}, title)
	if safe == "" {
		safe = "download"
	}
	return safe + ".nzb"
}

// sabSlots covers both queue and history slot shapes (numbers arrive as
// strings). Notable quirk: SABnzbd's own API names the category field
// differently between the two — "cat" in the queue, "category" in history —
// so both are carried here and each loop below reads its own.
type sabSlot struct {
	NzoID       string `json:"nzo_id"`
	Filename    string `json:"filename"` // queue
	Name        string `json:"name"`     // history
	Status      string `json:"status"`
	Percentage  string `json:"percentage"`
	Storage     string `json:"storage"`
	FailMessage string `json:"fail_message"`
	Cat         string `json:"cat"`      // queue
	Category    string `json:"category"` // history
}

func (s *sabnzbd) List(ctx context.Context) ([]Item, error) {
	var queue struct {
		Queue struct {
			Slots []sabSlot `json:"slots"`
		} `json:"queue"`
	}
	if err := s.api(ctx, url.Values{"mode": {"queue"}, "cat": {s.cfg.Category}}, &queue); err != nil {
		return nil, err
	}
	var history struct {
		History struct {
			Slots []sabSlot `json:"slots"`
		} `json:"history"`
	}
	if err := s.api(ctx, url.Values{"mode": {"history"}, "limit": {"100"}, "category": {s.cfg.Category}}, &history); err != nil {
		return nil, err
	}

	items := []Item{}
	for _, slot := range queue.Queue.Slots {
		if !strings.EqualFold(slot.Cat, s.cfg.Category) {
			// The category query param above is a server-side filter some
			// SABnzbd-compatible bridges (debrid services in particular) don't
			// actually honor, silently returning every app's downloads —
			// filter client-side too so another app's items sharing this
			// client never show up as ours.
			continue
		}
		item := Item{Client: s.cfg.Name, ConfigID: s.cfg.ID, ID: slot.NzoID, Title: slot.Filename}
		if pct, err := strconv.ParseFloat(slot.Percentage, 64); err == nil {
			item.Progress = pct / 100
		}
		switch strings.ToLower(slot.Status) {
		case "paused":
			item.Status = "paused"
		case "queued", "grabbing":
			item.Status = "queued"
		default:
			item.Status = "downloading"
		}
		items = append(items, item)
	}
	for _, slot := range history.History.Slots {
		if !strings.EqualFold(slot.Category, s.cfg.Category) {
			continue // see the queue loop above
		}
		item := Item{Client: s.cfg.Name, ConfigID: s.cfg.ID, ID: slot.NzoID, Title: slot.Name, Path: slot.Storage}
		switch strings.ToLower(slot.Status) {
		case "completed":
			item.Status = "completed"
			item.Progress = 1
		case "failed":
			item.Status = "failed"
		default:
			// Verifying/Repairing/Extracting — post-processing counts as
			// still downloading.
			item.Status = "downloading"
			item.Progress = 1
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *sabnzbd) Remove(ctx context.Context, id string, deleteData bool) error {
	// The id lives in either the queue or the history; delete is idempotent
	// so hit both.
	if err := s.api(ctx, url.Values{"mode": {"queue"}, "name": {"delete"}, "value": {id}}, nil); err != nil {
		return err
	}
	params := url.Values{"mode": {"history"}, "name": {"delete"}, "value": {id}}
	if deleteData {
		params.Set("del_files", "1")
	}
	return s.api(ctx, params, nil)
}

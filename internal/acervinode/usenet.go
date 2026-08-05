package acervinode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// AddNZBByURL adds nzbURL to AcerviNode via the SABnzbd shim's
// mode=addurl and returns the resulting nzo_id — the identifier
// GetUsenetStatus polls on.
func (c *Client) AddNZBByURL(ctx context.Context, nzbURL, displayName string) (nzoID string, err error) {
	form := url.Values{"mode": {"addurl"}, "name": {nzbURL}, "cat": {musicCategory}, "nzbname": {displayName}}
	resp, err := c.doSABnzbd(ctx, http.MethodPost, form, nil, "")
	if err != nil {
		return "", err
	}
	return parseAddNZBResponse(resp)
}

// AddNZBByFile uploads a .nzb file's raw bytes to AcerviNode via the
// SABnzbd shim's mode=addfile.
func (c *Client) AddNZBByFile(ctx context.Context, filename string, data []byte, displayName string) (nzoID string, err error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	// "name" is real SABnzbd's own (slightly confusing) field name for
	// the file part itself in addfile mode — see docs/sabnzbd-api.md.
	part, err := mw.CreateFormFile("name", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	form := url.Values{"mode": {"addfile"}, "cat": {musicCategory}, "nzbname": {displayName}}
	resp, err := c.doSABnzbd(ctx, http.MethodPost, form, &body, mw.FormDataContentType())
	if err != nil {
		return "", err
	}
	return parseAddNZBResponse(resp)
}

type addNZBResponse struct {
	Status bool     `json:"status"`
	NzoIDs []string `json:"nzo_ids"`
	Error  string   `json:"error"`
}

func parseAddNZBResponse(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	var ar addNZBResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", fmt.Errorf("decode add nzb response: %w", err)
	}
	if !ar.Status || len(ar.NzoIDs) == 0 {
		msg := ar.Error
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("acervinode: add nzb failed: %s", msg)
	}
	return ar.NzoIDs[0], nil
}

type queueResponse struct {
	Queue struct {
		Slots []struct {
			NzoID string `json:"nzo_id"`
		} `json:"slots"`
	} `json:"queue"`
}

type historyResponse struct {
	History struct {
		Slots []struct {
			NzoID       string `json:"nzo_id"`
			Status      string `json:"status"`
			Storage     string `json:"storage"`
			FailMessage string `json:"fail_message"`
		} `json:"slots"`
	} `json:"history"`
}

// GetUsenetStatus polls AcerviNode for nzoID's current status: checks
// the active queue first (still downloading, in whatever sub-phase),
// then history (done, one way or the other) — see docs/sabnzbd-api.md's
// state mapping. Returns ErrNotFound if nzoID appears in neither.
func (c *Client) GetUsenetStatus(ctx context.Context, nzoID string) (*Status, error) {
	queueResp, err := c.doSABnzbd(ctx, http.MethodGet, url.Values{"mode": {"queue"}}, nil, "")
	if err != nil {
		return nil, err
	}
	var q queueResponse
	if err := decodeSABnzbdJSON(queueResp, &q); err != nil {
		return nil, fmt.Errorf("decode queue response: %w", err)
	}
	for _, slot := range q.Queue.Slots {
		if slot.NzoID == nzoID {
			return &Status{State: StateDownloading}, nil
		}
	}

	historyResp, err := c.doSABnzbd(ctx, http.MethodGet, url.Values{"mode": {"history"}}, nil, "")
	if err != nil {
		return nil, err
	}
	var h historyResponse
	if err := decodeSABnzbdJSON(historyResp, &h); err != nil {
		return nil, fmt.Errorf("decode history response: %w", err)
	}
	for _, slot := range h.History.Slots {
		if slot.NzoID != nzoID {
			continue
		}
		if slot.Status == "Completed" {
			return &Status{State: StateCompleted, LocalPath: slot.Storage}, nil
		}
		return &Status{State: StateError, ErrorMessage: slot.FailMessage}, nil
	}

	return nil, ErrNotFound
}

func decodeSABnzbdJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

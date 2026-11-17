// Package blobstore is the core-api client for the blob-adapter — the
// Phase 49 sidecar that fronts Apache OpenDAL. OpenDAL is the storage
// engine (fs/memory/s3/gcs/azblob/... selected by adapter env); this
// client is just the HTTP boundary, identical in spirit to the Mojaloop /
// email / voice adapter clients.
//
// Semantics the store wrapper relies on:
//   - Put overwrites (media ids are uuids, so collisions don't happen);
//     adapter failure fails the upload — no silent durability downgrade.
//   - Get maps adapter 404 to httpx.ErrNotFound.
//   - Delete is idempotent (adapter returns 204 for missing keys).
package blobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
)

type Client struct {
	base string
	hc   *http.Client
}

// New builds a client for the blob-adapter at baseURL (e.g.
// http://blob-adapter:8230). Timeouts are generous: media payloads run to
// 8 MB and the adapter may be writing to a remote object store.
func New(baseURL string) *Client {
	return &Client{base: baseURL, hc: &http.Client{Timeout: 30 * time.Second}}
}

// EngineInfo is the adapter's provenance report (GET /v1/engine).
type EngineInfo struct {
	Engine string `json:"engine"` // "apache-opendal"
	Scheme string `json:"scheme"` // fs | memory | s3 | gcs | azblob | ...
}

func (c *Client) do(ctx context.Context, method, tenantID, key string, body []byte, contentType string) (*http.Response, error) {
	url := c.base + "/v1/blobs/" + key
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Tenant-ID", tenantID)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blob-adapter %s %s: %w", method, key, err)
	}
	return res, nil
}

// Put stores data under key for the tenant. Fail-closed: an adapter error
// propagates — the caller must NOT silently fall back to inline storage,
// because that would be an unlabeled durability downgrade.
func (c *Client) Put(ctx context.Context, tenantID, key string, data []byte, contentType string) error {
	res, err := c.do(ctx, http.MethodPut, tenantID, key, data, contentType)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		return fmt.Errorf("blob-adapter put %s: status %d", key, res.StatusCode)
	}
	return nil
}

// Get fetches the object. A missing key returns httpx.ErrNotFound.
func (c *Client) Get(ctx context.Context, tenantID, key string) ([]byte, string, error) {
	res, err := c.do(ctx, http.MethodGet, tenantID, key, nil, "")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, "", httpx.ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("blob-adapter get %s: status %d", key, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, "", err
	}
	return data, res.Header.Get("Content-Type"), nil
}

// Delete removes the object. Idempotent by adapter contract.
func (c *Client) Delete(ctx context.Context, tenantID, key string) error {
	res, err := c.do(ctx, http.MethodDelete, tenantID, key, nil, "")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fmt.Errorf("blob-adapter delete %s: status %d", key, res.StatusCode)
	}
	return nil
}

// Engine queries the adapter's provenance endpoint. It carries its own
// short timeout so a sick adapter cannot wedge the health/engine surfaces.
func (c *Client) Engine(ctx context.Context) (*EngineInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/engine", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blob-adapter engine: status %d", res.StatusCode)
	}
	var info EngineInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

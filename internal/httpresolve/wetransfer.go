// Package httpresolve provides resolvers that convert share links from
// file-transfer services into direct download URLs.
package httpresolve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	wtUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:102.0) Gecko/20100101 Firefox/102.0"
	wtAPIURL    = "https://wetransfer.com/api/v4/transfers/%s/download"
)

// ResolveDirectLink resolves a WeTransfer share URL to a direct download URL
// and a fallback filename derived from the transfer ID.
//
// Accepted share URL shapes (host must be wetransfer.com or a .wetransfer.com
// subdomain):
//
//	/downloads/<transfer_id>/<security_hash>
//	/downloads/<transfer_id>/<recipient_id>/<security_hash>
//
// NOTE on path-segment order: as of April 2026 WeTransfer's 4-segment share
// links put the recipient_id in position 2 and the (short) security_hash last.
// This differs from the 3-segment form and from older documented conventions.
//
// The implementation calls POST /api/v4/transfers/<id>/download directly — no
// CSRF handshake is required. The browser-side flow sends Origin and
// x-requested-with headers; the API responds with a short-lived CloudFront
// signed direct_link (~10 minute TTL observed).
//
// If client is nil, a new *http.Client with a 30 s timeout and a cookie jar is
// created internally. A non-nil client is not mutated; if it lacks a jar one is
// wrapped locally (WeTransfer no longer requires a session cookie for this
// endpoint, but the jar is cheap insurance against future re-introductions).
func ResolveDirectLink(ctx context.Context, client *http.Client, shareURL string) (directURL, filename string, err error) {
	transferID, secHash, recipientID, parseErr := parseWeTransferURL(shareURL)
	if parseErr != nil {
		return "", "", parseErr
	}

	client = ensureJar(client)

	directURL, err = postDownload(ctx, client, transferID, secHash, recipientID, shareURL)
	if err != nil {
		return "", "", err
	}

	return directURL, transferID, nil
}

// parseWeTransferURL validates the URL and extracts the three components the
// download API needs. For a 3-segment path the second segment is the security
// hash and recipientID is empty; for a 4-segment path the second segment is
// the recipient_id and the third is the security_hash (new WeTransfer layout).
func parseWeTransferURL(rawURL string) (transferID, secHash, recipientID string, err error) {
	parsed, parseErr := url.Parse(rawURL)
	if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	if !isWeTransferHost(parsed.Host) {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(segments) < 3 || segments[0] != "downloads" {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	transferID = segments[1]
	if transferID == "" {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	switch {
	case len(segments) == 3:
		secHash = segments[2]
	case len(segments) >= 4 && segments[3] != "":
		recipientID = segments[2]
		secHash = segments[3]
	default:
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	if secHash == "" {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	return transferID, secHash, recipientID, nil
}

// IsWeTransferHost reports whether host is wetransfer.com or a direct
// subdomain thereof (e.g., foo.wetransfer.com). It strips any port suffix
// before comparing. It is exported so callers can gate the resolver without
// re-implementing the check.
func IsWeTransferHost(host string) bool {
	return isWeTransferHost(host)
}

func isWeTransferHost(host string) bool {
	if i := strings.IndexByte(host, ':'); i != -1 {
		host = host[:i]
	}
	return host == "wetransfer.com" || strings.HasSuffix(host, ".wetransfer.com")
}

// downloadRequest is the POST body sent to the WeTransfer download API.
type downloadRequest struct {
	Intent       string `json:"intent"`
	SecurityHash string `json:"security_hash"`
	RecipientID  string `json:"recipient_id,omitempty"`
}

// downloadResponse is the expected JSON response from the WeTransfer download API.
type downloadResponse struct {
	DirectLink string `json:"direct_link"`
}

// postDownload calls POST /api/v4/transfers/{id}/download and returns the
// direct link. shareURL is used to derive the Origin + Referer headers that
// WeTransfer's API expects.
func postDownload(ctx context.Context, client *http.Client, transferID, secHash, recipientID, shareURL string) (string, error) {
	reqBody := downloadRequest{
		Intent:       "entire_transfer",
		SecurityHash: secHash,
	}
	if recipientID != "" {
		reqBody.RecipientID = recipientID
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("could not marshal download request: %w", err)
	}

	apiURL := fmt.Sprintf(wtAPIURL, transferID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("could not build download request: %w", err)
	}
	req.Header.Set("User-Agent", wtUserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	if origin := originFromShareURL(shareURL); origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", shareURL)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download request to wetransfer failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("could not read wetransfer download response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		preview := respBody
		if len(preview) > 256 {
			preview = preview[:256]
		}
		return "", fmt.Errorf("wetransfer download API returned status %d: %s", resp.StatusCode, preview)
	}

	var dlResp downloadResponse
	if err := json.Unmarshal(respBody, &dlResp); err != nil {
		return "", fmt.Errorf("could not parse wetransfer download response: %w", err)
	}

	if dlResp.DirectLink == "" {
		return "", fmt.Errorf("wetransfer download response contained no direct_link")
	}

	return dlResp.DirectLink, nil
}

// originFromShareURL returns scheme://host for the share URL, or "" if parsing fails.
func originFromShareURL(shareURL string) string {
	parsed, err := url.Parse(shareURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// ensureJar returns the client unchanged if it already has a cookie jar,
// or returns a shallow copy with a freshly-created jar if not. The caller's
// client is never mutated.
func ensureJar(client *http.Client) *http.Client {
	if client == nil {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 30 * time.Second}
	}
	if client.Jar != nil {
		return client
	}
	jar, _ := cookiejar.New(nil)
	copy := *client
	copy.Jar = jar
	return &copy
}

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
	"regexp"
	"strings"
	"time"
)

const (
	wtUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:102.0) Gecko/20100101 Firefox/102.0"
	wtHomeURL   = "https://wetransfer.com/"
	wtAPIURL    = "https://wetransfer.com/api/v4/transfers/%s/download"
)

var csrfRegex = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// ResolveDirectLink resolves a WeTransfer share URL to a direct download URL
// and a fallback filename (<transfer_id>.zip).
//
// shareURL must have a host whose suffix is "wetransfer.com" and a path of the
// form /downloads/<transfer_id>/<security_hash> or
// /downloads/<transfer_id>/<security_hash>/<recipient_id>.
//
// If client is nil, a new *http.Client with a cookie jar and 30 s timeout is
// created internally. If client is non-nil it MUST have a non-nil Jar field so
// that the CSRF session cookie set by GET wetransfer.com/ persists into the
// subsequent POST request. Passing a client without a Jar will cause the CSRF
// flow to fail; to avoid silent breakage this function wraps a jarless client
// with a local jar for the duration of the call rather than mutating the
// caller's client.
func ResolveDirectLink(ctx context.Context, client *http.Client, shareURL string) (directURL, filename string, err error) {
	transferID, secHash, recipientID, parseErr := parseWeTransferURL(shareURL)
	if parseErr != nil {
		return "", "", parseErr
	}

	client = ensureJar(client)

	csrfToken, err := fetchCSRF(ctx, client)
	if err != nil {
		return "", "", err
	}

	directURL, err = postDownload(ctx, client, transferID, secHash, recipientID, csrfToken)
	if err != nil {
		return "", "", err
	}

	return directURL, transferID + ".zip", nil
}

// parseWeTransferURL validates the URL and extracts transfer components.
// recipientID is empty string when the path only has two segments after /downloads/.
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
	secHash = segments[2]
	if transferID == "" || secHash == "" {
		return "", "", "", fmt.Errorf("not a wetransfer share URL: %s", rawURL)
	}

	if len(segments) >= 4 && segments[3] != "" {
		recipientID = segments[3]
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

// fetchCSRF performs GET https://wetransfer.com/ and extracts the CSRF token.
func fetchCSRF(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wtHomeURL, nil)
	if err != nil {
		return "", fmt.Errorf("could not build wetransfer home request: %w", err)
	}
	req.Header.Set("User-Agent", wtUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach wetransfer.com: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("could not read wetransfer.com response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		preview := body
		if len(preview) > 256 {
			preview = preview[:256]
		}
		return "", fmt.Errorf("wetransfer.com returned status %d: %s", resp.StatusCode, preview)
	}

	matches := csrfRegex.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("could not find csrf-token in wetransfer.com response")
	}
	return string(matches[1]), nil
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

// postDownload calls POST /api/v4/transfers/{id}/download and returns the direct link.
func postDownload(ctx context.Context, client *http.Client, transferID, secHash, recipientID, csrfToken string) (string, error) {
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

	url := fmt.Sprintf(wtAPIURL, transferID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("could not build download request: %w", err)
	}
	req.Header.Set("User-Agent", wtUserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-csrf-token", csrfToken)
	req.Header.Set("x-requested-with", "XMLHttpRequest")

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
	// Wrap in a local copy with a jar so CSRF session cookie persists.
	jar, _ := cookiejar.New(nil)
	copy := *client
	copy.Jar = jar
	return &copy
}

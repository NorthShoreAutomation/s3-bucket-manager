package httpresolve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc allows constructing an http.RoundTripper from a plain function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// makeResponse is a convenience helper that returns a 200 response with the
// given body string.
func makeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// csrfHTML is a minimal WeTransfer home page response containing a CSRF token.
const csrfHTML = `<html><head><meta name="csrf-token" content="CSRFTOKEN123"></head></html>`

// happyPathTransport stubs exactly two endpoints used by ResolveDirectLink.
func happyPathTransport(t *testing.T, expectRecipientID string) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.String() == "https://wetransfer.com/":
			return makeResponse(http.StatusOK, csrfHTML), nil

		case r.Method == http.MethodPost &&
			r.URL.String() == "https://wetransfer.com/api/v4/transfers/TRANSFERID/download":

			// Verify CSRF header
			if got := r.Header.Get("x-csrf-token"); got != "CSRFTOKEN123" {
				t.Errorf("x-csrf-token = %q; want CSRFTOKEN123", got)
			}
			if got := r.Header.Get("x-requested-with"); got != "XMLHttpRequest" {
				t.Errorf("x-requested-with = %q; want XMLHttpRequest", got)
			}

			// Verify request body
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("could not read POST body: %v", err)
			}
			var payload map[string]string
			if err := json.Unmarshal(bodyBytes, &payload); err != nil {
				t.Fatalf("POST body is not valid JSON: %v", err)
			}
			if got := payload["intent"]; got != "entire_transfer" {
				t.Errorf("intent = %q; want entire_transfer", got)
			}
			if got := payload["security_hash"]; got != "HASH" {
				t.Errorf("security_hash = %q; want HASH", got)
			}
			if expectRecipientID != "" {
				if got := payload["recipient_id"]; got != expectRecipientID {
					t.Errorf("recipient_id = %q; want %q", got, expectRecipientID)
				}
			} else {
				if _, ok := payload["recipient_id"]; ok {
					t.Errorf("recipient_id present in body but should be absent")
				}
			}

			return makeResponse(http.StatusOK, `{"direct_link":"https://download.example/abc"}`), nil

		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			return nil, nil
		}
	}
}

func clientWithTransport(rt http.RoundTripper) *http.Client {
	// We intentionally omit a Jar here to exercise the ensureJar wrapping path.
	return &http.Client{Transport: rt}
}

func TestResolveDirectLink_happyPath(t *testing.T) {
	client := clientWithTransport(happyPathTransport(t, ""))

	directURL, filename, err := ResolveDirectLink(
		context.Background(),
		client,
		"https://foo.wetransfer.com/downloads/TRANSFERID/HASH",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if directURL != "https://download.example/abc" {
		t.Errorf("directURL = %q; want https://download.example/abc", directURL)
	}
	if filename != "TRANSFERID.zip" {
		t.Errorf("filename = %q; want TRANSFERID.zip", filename)
	}
}

func TestResolveDirectLink_withRecipientID(t *testing.T) {
	client := clientWithTransport(happyPathTransport(t, "RID"))

	directURL, filename, err := ResolveDirectLink(
		context.Background(),
		client,
		"https://foo.wetransfer.com/downloads/TRANSFERID/HASH/RID",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if directURL != "https://download.example/abc" {
		t.Errorf("directURL = %q; want https://download.example/abc", directURL)
	}
	if filename != "TRANSFERID.zip" {
		t.Errorf("filename = %q; want TRANSFERID.zip", filename)
	}
}

func TestResolveDirectLink_rejectsNonWetransferHost(t *testing.T) {
	hostile := []string{
		"https://example.com/downloads/a/b",
		"https://evilwetransfer.com/downloads/a/b",     // suffix-spoof: must be .wetransfer.com or exact
		"https://notwetransfer.com/downloads/a/b",      // suffix-spoof variant
		"https://wetransfer.com.evil.io/downloads/a/b", // double-label suffix
	}
	for _, u := range hostile {
		_, _, err := ResolveDirectLink(context.Background(), nil, u)
		if err == nil {
			t.Errorf("expected error for host %q, got nil", u)
		}
	}
}

func TestIsWeTransferHost(t *testing.T) {
	cases := map[string]bool{
		"wetransfer.com":                      true,
		"foo.wetransfer.com":                  true,
		"northshoreautomation.wetransfer.com": true,
		"wetransfer.com:443":                  true,
		"evilwetransfer.com":                  false,
		"wetransfer.com.evil.io":              false,
		"notwetransfer.com":                   false,
		"":                                    false,
	}
	for host, want := range cases {
		if got := IsWeTransferHost(host); got != want {
			t.Errorf("IsWeTransferHost(%q) = %v; want %v", host, got, want)
		}
	}
}

func TestResolveDirectLink_rejectsMalformedPath(t *testing.T) {
	cases := []string{
		"https://wetransfer.com/downloads/onlyOne",
		"https://wetransfer.com/wrong/prefix/a/b",
	}
	for _, url := range cases {
		_, _, err := ResolveDirectLink(context.Background(), nil, url)
		if err == nil {
			t.Errorf("expected error for URL %q, got nil", url)
		}
	}
}

func TestResolveDirectLink_missingCSRF(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.String() == "https://wetransfer.com/" {
			return makeResponse(http.StatusOK, "<html>no meta tag here</html>"), nil
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		return nil, nil
	})

	_, _, err := ResolveDirectLink(
		context.Background(),
		clientWithTransport(rt),
		"https://wetransfer.com/downloads/TID/HASH",
	)
	if err == nil {
		t.Fatal("expected error when CSRF token is missing, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "csrf") {
		t.Errorf("error %q does not mention csrf", err.Error())
	}
}

func TestResolveDirectLink_non200FromGet(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.String() == "https://wetransfer.com/" {
			return makeResponse(http.StatusInternalServerError, "boom"), nil
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		return nil, nil
	})

	_, _, err := ResolveDirectLink(
		context.Background(),
		clientWithTransport(rt),
		"https://wetransfer.com/downloads/TID/HASH",
	)
	if err == nil {
		t.Fatal("expected error for 500 GET response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not contain status 500", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not contain body preview", err.Error())
	}
}

func TestResolveDirectLink_emptyDirectLink(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.String() == "https://wetransfer.com/":
			return makeResponse(http.StatusOK, csrfHTML), nil
		case r.Method == http.MethodPost:
			return makeResponse(http.StatusOK, `{"direct_link":""}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			return nil, nil
		}
	})

	_, _, err := ResolveDirectLink(
		context.Background(),
		clientWithTransport(rt),
		"https://wetransfer.com/downloads/TID/HASH",
	)
	if err == nil {
		t.Fatal("expected error when direct_link is empty, got nil")
	}
}

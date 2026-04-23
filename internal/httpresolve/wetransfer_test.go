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

// makeResponse returns a response with the given status and body.
func makeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// happyPathTransport stubs the single POST /download endpoint that
// ResolveDirectLink invokes. expectSecHash is the security_hash value the
// request body is asserted against; expectRecipientID is the recipient_id,
// or empty when the share URL does not include one.
func happyPathTransport(t *testing.T, transferID, expectSecHash, expectRecipientID, expectOrigin string) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.String() == "https://wetransfer.com/api/v4/transfers/"+transferID+"/download":

			if got := r.Header.Get("x-requested-with"); got != "XMLHttpRequest" {
				t.Errorf("x-requested-with = %q; want XMLHttpRequest", got)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q; want application/json", got)
			}
			if expectOrigin != "" {
				if got := r.Header.Get("Origin"); got != expectOrigin {
					t.Errorf("Origin = %q; want %q", got, expectOrigin)
				}
				if got := r.Header.Get("Referer"); got == "" {
					t.Errorf("Referer header missing")
				}
			}

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
			if got := payload["security_hash"]; got != expectSecHash {
				t.Errorf("security_hash = %q; want %q", got, expectSecHash)
			}
			if expectRecipientID != "" {
				if got := payload["recipient_id"]; got != expectRecipientID {
					t.Errorf("recipient_id = %q; want %q", got, expectRecipientID)
				}
			} else if _, ok := payload["recipient_id"]; ok {
				t.Errorf("recipient_id present in body but should be absent")
			}

			return makeResponse(http.StatusOK, `{"direct_link":"https://download.example/abc"}`), nil

		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
			return nil, nil
		}
	}
}

func clientWithTransport(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt}
}

func TestResolveDirectLink_threeSegmentShare(t *testing.T) {
	client := clientWithTransport(happyPathTransport(t, "TRANSFERID", "HASH", "", "https://foo.wetransfer.com"))

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
	if filename != "TRANSFERID" {
		t.Errorf("filename = %q; want TRANSFERID", filename)
	}
}

// TestResolveDirectLink_fourSegmentShare verifies the current WeTransfer
// 4-segment layout: /downloads/<transfer>/<recipient>/<security_hash>.
func TestResolveDirectLink_fourSegmentShare(t *testing.T) {
	client := clientWithTransport(happyPathTransport(
		t,
		"TRANSFERID",
		"d3bf30",
		"RECIPIENT46HEX",
		"https://northshoreautomation.wetransfer.com",
	))

	directURL, _, err := ResolveDirectLink(
		context.Background(),
		client,
		"https://northshoreautomation.wetransfer.com/downloads/TRANSFERID/RECIPIENT46HEX/d3bf30",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if directURL != "https://download.example/abc" {
		t.Errorf("directURL = %q; want https://download.example/abc", directURL)
	}
}

func TestResolveDirectLink_rejectsNonWetransferHost(t *testing.T) {
	hostile := []string{
		"https://example.com/downloads/a/b",
		"https://evilwetransfer.com/downloads/a/b",
		"https://notwetransfer.com/downloads/a/b",
		"https://wetransfer.com.evil.io/downloads/a/b",
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
	for _, rawURL := range cases {
		_, _, err := ResolveDirectLink(context.Background(), nil, rawURL)
		if err == nil {
			t.Errorf("expected error for URL %q, got nil", rawURL)
		}
	}
}

func TestResolveDirectLink_apiNon200(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return makeResponse(http.StatusBadRequest, "boom"), nil
	})

	_, _, err := ResolveDirectLink(
		context.Background(),
		clientWithTransport(rt),
		"https://wetransfer.com/downloads/TID/HASH",
	)
	if err == nil {
		t.Fatal("expected error for 400 POST response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error %q does not contain status 400", err.Error())
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not contain body preview", err.Error())
	}
}

func TestResolveDirectLink_emptyDirectLink(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return makeResponse(http.StatusOK, `{"direct_link":""}`), nil
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

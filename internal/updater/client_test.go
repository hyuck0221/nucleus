package updater

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCheckBypassesCachesAndFindsNewRelease(t *testing.T) {
	client := &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("nocache") == "" {
			t.Error("latest release request has no cache buster")
		}
		if req.Header.Get("Cache-Control") != "no-cache" || req.Header.Get("Pragma") != "no-cache" {
			t.Errorf("missing cache-control headers: %#v", req.Header)
		}
		body := `{"tag_name":"v1.0.12","html_url":"https://github.com/hyuck0221/nucleus/releases/tag/v1.0.12","assets":[{"name":"Nucleus-v1.0.12-darwin-arm64.dmg","browser_download_url":"https://example.test/Nucleus.dmg"}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result := client.Check(context.Background(), "v1.0.11")
	if !result.Available || result.Latest != "v1.0.12" || result.Error != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCheckReportsMissingDMG(t *testing.T) {
	client := &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"tag_name":"v1.0.12","assets":[{"name":"checksums.txt","browser_download_url":"https://example.test/checksums.txt"}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}

	result := client.Check(context.Background(), "v1.0.11")
	if result.Available || !strings.Contains(result.Error, "no compatible DMG") {
		t.Fatalf("unexpected result: %#v", result)
	}
}

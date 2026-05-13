package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const repo = "hyuck0221/nucleus"

type Client struct {
	HTTPClient *http.Client
	mu         sync.RWMutex
	state      DownloadState
}

type CheckResult struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	AssetName string `json:"assetName,omitempty"`
	AssetURL  string `json:"assetUrl,omitempty"`
	PageURL   string `json:"pageUrl,omitempty"`
	Error     string `json:"error,omitempty"`
}

type DownloadState struct {
	Active     bool   `json:"active"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
	Version    string `json:"version,omitempty"`
	AssetName  string `json:"assetName,omitempty"`
	Path       string `json:"path,omitempty"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	Percent    int    `json:"percent"`
}

type release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func New() *Client {
	return &Client{HTTPClient: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) Check(ctx context.Context, current string) CheckResult {
	result := CheckResult{Current: current}
	if current == "" || current == "dev" {
		return result
	}
	latest, err := c.latestRelease(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Latest = latest.TagName
	result.PageURL = latest.HTMLURL
	chosen := selectDMG(latest.Assets)
	if chosen.Name != "" {
		result.AssetName = chosen.Name
		result.AssetURL = chosen.BrowserDownloadURL
	}
	result.Available = newer(latest.TagName, current) && chosen.BrowserDownloadURL != ""
	return result
}

func (c *Client) StartDownload(ctx context.Context, current string) DownloadState {
	check := c.Check(ctx, current)
	if check.Error != "" {
		return DownloadState{Error: check.Error}
	}
	if !check.Available {
		return DownloadState{Error: "no update available"}
	}
	state := DownloadState{Active: true, Version: check.Latest, AssetName: check.AssetName}
	c.setState(state)
	go c.downloadAndOpen(check)
	return state
}

func (c *Client) State() DownloadState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) latestRelease(ctx context.Context) (release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nucleus-updater")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return release{}, fmt.Errorf("github returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var latest release
	return latest, json.NewDecoder(resp.Body).Decode(&latest)
}

func (c *Client) downloadAndOpen(check CheckResult) {
	req, _ := http.NewRequest(http.MethodGet, check.AssetURL, nil)
	req.Header.Set("User-Agent", "nucleus-updater")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.fail(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		c.fail(fmt.Errorf("download returned %s", resp.Status))
		return
	}
	dir := filepath.Join(os.TempDir(), "nucleus-updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.fail(err)
		return
	}
	path := filepath.Join(dir, check.AssetName)
	file, err := os.Create(path)
	if err != nil {
		c.fail(err)
		return
	}
	defer file.Close()
	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 1024*256)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				c.fail(err)
				return
			}
			downloaded += int64(n)
			c.updateProgress(downloaded, total, path)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			c.fail(readErr)
			return
		}
	}
	_ = file.Close()
	c.finish(path)
	if err := exec.Command("open", path).Start(); err != nil {
		c.fail(err)
		return
	}
	time.AfterFunc(3*time.Second, func() {
		os.Exit(0)
	})
}

func (c *Client) setState(state DownloadState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

func (c *Client) updateProgress(downloaded, total int64, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Active = true
	c.state.Downloaded = downloaded
	c.state.Total = total
	c.state.Path = path
	if total > 0 {
		c.state.Percent = int(downloaded * 100 / total)
	}
}

func (c *Client) finish(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Active = false
	c.state.Done = true
	c.state.Path = path
	c.state.Percent = 100
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Active = false
	c.state.Error = err.Error()
}

func selectDMG(assets []asset) asset {
	needle := "darwin-" + runtime.GOARCH + ".dmg"
	for _, item := range assets {
		if strings.HasSuffix(item.Name, needle) {
			return item
		}
	}
	for _, item := range assets {
		if strings.HasSuffix(item.Name, ".dmg") {
			return item
		}
	}
	return asset{}
}

func newer(latest, current string) bool {
	return compareVersions(latest, current) > 0
}

func compareVersions(a, b string) int {
	aa := versionParts(a)
	bb := versionParts(b)
	for i := 0; i < 3; i++ {
		if aa[i] > bb[i] {
			return 1
		}
		if aa[i] < bb[i] {
			return -1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		fmt.Sscanf(parts[i], "%d", &out[i])
	}
	return out
}

package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Status struct {
	Installed bool   `json:"installed"`
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
	BaseURL   string `json:"baseUrl"`
	Error     string `json:"error,omitempty"`
}

type Model struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
	Digest     string    `json:"digest"`
}

func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 0},
	}
}

func (c *Client) Status(ctx context.Context) Status {
	status := Status{BaseURL: c.BaseURL}
	if out, err := exec.CommandContext(ctx, "ollama", "--version").CombinedOutput(); err == nil {
		status.Installed = true
		status.Version = strings.TrimSpace(string(out))
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer resp.Body.Close()
	status.Reachable = resp.StatusCode >= 200 && resp.StatusCode < 500
	if !status.Reachable {
		status.Error = resp.Status
	}
	return status
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %s: %s", resp.Status, string(body))
	}
	var parsed struct {
		Models []Model `json:"models"`
	}
	return parsed.Models, json.NewDecoder(resp.Body).Decode(&parsed)
}

func (c *Client) Pull(ctx context.Context, model string, progress func([]byte)) error {
	payload, _ := json.Marshal(map[string]string{"name": model})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/pull", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %s: %s", resp.Status, string(body))
	}
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if progress != nil {
			progress(raw)
		}
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, model string) error {
	payload, _ := json.Marshal(map[string]string{"name": model})
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/api/delete", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %s: %s", resp.Status, string(body))
	}
	return nil
}

func (c *Client) ProxyOpenAI(ctx context.Context, body io.Reader) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	return c.HTTPClient.Do(req)
}

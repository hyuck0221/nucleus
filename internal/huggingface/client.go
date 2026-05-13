package huggingface

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Model struct {
	ID          string    `json:"id"`
	PullName    string    `json:"pullName"`
	URL         string    `json:"url"`
	Downloads   int       `json:"downloads"`
	Likes       int       `json:"likes"`
	Tags        []string  `json:"tags"`
	PipelineTag string    `json:"pipelineTag,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

func New() *Client {
	return &Client{
		BaseURL:    "https://huggingface.co",
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) SearchModels(ctx context.Context, query string, limit int) ([]Model, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	q := strings.TrimSpace(query)
	if q == "" {
		q = "GGUF"
	} else if !strings.Contains(strings.ToLower(q), "gguf") {
		q += " GGUF"
	}
	endpoint, _ := url.Parse(c.BaseURL + "/api/models")
	params := endpoint.Query()
	params.Set("search", q)
	params.Set("limit", fmt.Sprintf("%d", limit))
	endpoint.RawQuery = params.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("huggingface returned %s", resp.Status)
	}
	var raw []struct {
		ID          string    `json:"id"`
		ModelID     string    `json:"modelId"`
		Downloads   int       `json:"downloads"`
		Likes       int       `json:"likes"`
		Tags        []string  `json:"tags"`
		PipelineTag string    `json:"pipeline_tag"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(raw))
	for _, item := range raw {
		id := item.ID
		if id == "" {
			id = item.ModelID
		}
		if id == "" || !isGGUF(id, item.Tags) {
			continue
		}
		models = append(models, Model{
			ID:          id,
			PullName:    "hf.co/" + id,
			URL:         c.BaseURL + "/" + id,
			Downloads:   item.Downloads,
			Likes:       item.Likes,
			Tags:        item.Tags,
			PipelineTag: item.PipelineTag,
			CreatedAt:   item.CreatedAt,
		})
	}
	return models, nil
}

func isGGUF(id string, tags []string) bool {
	if strings.Contains(strings.ToLower(id), "gguf") {
		return true
	}
	for _, tag := range tags {
		if strings.EqualFold(tag, "gguf") {
			return true
		}
	}
	return false
}

package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Ready(ctx context.Context) error {
	if c.baseURL == "" {
		return fmt.Errorf("reranker base_url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
	}

	_, err = c.Rerank(ctx, "health", []string{"health", "check"})
	if err != nil {
		return fmt.Errorf("reranker health check failed: %w", err)
	}
	return nil
}

func (c *Client) Rerank(ctx context.Context, query string, texts []string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if len(texts) == 0 {
		return []Result{}, nil
	}

	body := map[string]any{
		"query":      query,
		"texts":      texts,
		"raw_scores": true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read rerank response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(bodyBytes))
		if message == "" {
			return nil, fmt.Errorf("rerank request failed: %s", resp.Status)
		}
		return nil, fmt.Errorf("rerank request failed: %s: %s", resp.Status, message)
	}

	var payload []Result
	if err := json.Unmarshal(bodyBytes, &payload); err == nil {
		return payload, nil
	}

	var wrapped struct {
		Results []Result `json:"results"`
		Data    []Result `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &wrapped); err != nil {
		return nil, fmt.Errorf("decode rerank response failed: %w", err)
	}
	if len(wrapped.Results) > 0 {
		return wrapped.Results, nil
	}
	if len(wrapped.Data) > 0 {
		return wrapped.Data, nil
	}
	return []Result{}, nil
}

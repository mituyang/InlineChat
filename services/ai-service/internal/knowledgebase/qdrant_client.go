package knowledgebase

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

type qdrantClient struct {
	baseURL    string
	apiKey     string
	collection string
	httpClient *http.Client
}

type qdrantSearchResult struct {
	ID         string
	Section    string
	Text       string
	SourcePath string
	Score      float64
}

func newQdrantClient(baseURL string, apiKey string, collection string, timeout time.Duration) *qdrantClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &qdrantClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		collection: strings.TrimSpace(collection),
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *qdrantClient) Ready(ctx context.Context) error {
	resp, err := c.doJSON(ctx, http.MethodGet, "/collections", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant collections unavailable: %s", resp.Status)
	}
	return nil
}

func (c *qdrantClient) ReplaceSite(ctx context.Context, siteID string, points []vectorPoint) error {
	if strings.TrimSpace(siteID) == "" {
		return fmt.Errorf("site_id is required")
	}
	if len(points) == 0 {
		return c.deleteSite(ctx, siteID)
	}

	if err := c.ensureCollection(ctx, len(points[0].Vector)); err != nil {
		return err
	}
	if err := c.deleteSite(ctx, siteID); err != nil {
		return err
	}
	for start := 0; start < len(points); start += 64 {
		end := start + 64
		if end > len(points) {
			end = len(points)
		}
		payloadPoints := make([]map[string]any, 0, end-start)
		for _, item := range points[start:end] {
			payloadPoints = append(payloadPoints, map[string]any{
				"id":     item.ID,
				"vector": item.Vector,
				"payload": map[string]any{
					"site_id":     item.SiteID,
					"section":     item.Section,
					"text":        item.Text,
					"source_path": item.SourcePath,
				},
			})
		}
		resp, err := c.doJSON(ctx, http.MethodPut, c.collectionPath("/points?wait=true"), map[string]any{
			"points": payloadPoints,
		})
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read qdrant upsert response failed: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := strings.TrimSpace(string(body))
			if message == "" {
				return fmt.Errorf("qdrant upsert failed: %s", resp.Status)
			}
			return fmt.Errorf("qdrant upsert failed: %s: %s", resp.Status, message)
		}
	}
	return nil
}

func (c *qdrantClient) Search(ctx context.Context, siteID string, vector []float64, limit int) ([]qdrantSearchResult, error) {
	if strings.TrimSpace(siteID) == "" {
		return nil, fmt.Errorf("site_id is required")
	}
	if len(vector) == 0 {
		return nil, fmt.Errorf("vector is required")
	}
	if limit <= 0 {
		limit = 10
	}

	resp, err := c.doJSON(ctx, http.MethodPost, c.collectionPath("/points/search"), map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key": "site_id",
					"match": map[string]any{
						"value": siteID,
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Result []struct {
			ID      any     `json:"id"`
			Score   float64 `json:"score"`
			Payload struct {
				Section    string `json:"section"`
				Text       string `json:"text"`
				SourcePath string `json:"source_path"`
			} `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode qdrant search response failed: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant search failed: %s", resp.Status)
	}

	results := make([]qdrantSearchResult, 0, len(payload.Result))
	for _, item := range payload.Result {
		results = append(results, qdrantSearchResult{
			ID:         fmt.Sprint(item.ID),
			Section:    item.Payload.Section,
			Text:       item.Payload.Text,
			SourcePath: item.Payload.SourcePath,
			Score:      item.Score,
		})
	}
	return results, nil
}

func (c *qdrantClient) ensureCollection(ctx context.Context, vectorSize int) error {
	resp, err := c.doJSON(ctx, http.MethodGet, c.collectionPath(""), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		createResp, createErr := c.doJSON(ctx, http.MethodPut, c.collectionPath(""), map[string]any{
			"vectors": map[string]any{
				"size":     vectorSize,
				"distance": "Cosine",
			},
		})
		if createErr != nil {
			return createErr
		}
		createResp.Body.Close()
		if createResp.StatusCode < 200 || createResp.StatusCode >= 300 {
			return fmt.Errorf("create qdrant collection failed: %s", createResp.Status)
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("get qdrant collection failed: %s", resp.Status)
	}
	return nil
}

func (c *qdrantClient) deleteSite(ctx context.Context, siteID string) error {
	resp, err := c.doJSON(ctx, http.MethodPost, c.collectionPath("/points/delete?wait=true"), map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key": "site_id",
					"match": map[string]any{
						"value": siteID,
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete site vectors failed: %s", resp.Status)
	}
	return nil
}

func (c *qdrantClient) collectionPath(suffix string) string {
	return "/collections/" + c.collection + suffix
}

func (c *qdrantClient) doJSON(ctx context.Context, method string, path string, body any) (*http.Response, error) {
	var raw []byte
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = payload
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	return c.httpClient.Do(req)
}

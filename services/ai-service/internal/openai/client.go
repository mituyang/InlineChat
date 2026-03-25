package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func New(baseURL string, model string, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Model() string {
	return c.model
}

func (c *Client) Ready(ctx context.Context) error {
	resp, err := c.doJSON(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("models endpoint unavailable: %s", resp.Status)
	}
	return nil
}

func (c *Client) CreateEmbeddings(ctx context.Context, inputs []string) ([][]float64, error) {
	if len(inputs) == 0 {
		return [][]float64{}, nil
	}
	body := map[string]any{
		"model": c.model,
		"input": inputs,
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode embeddings response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(payload.Error.Message) != "" {
			return nil, errors.New(payload.Error.Message)
		}
		return nil, fmt.Errorf("embeddings request failed: %s", resp.Status)
	}

	out := make([][]float64, len(inputs))
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(out) {
			continue
		}
		out[item.Index] = item.Embedding
	}
	for i := range out {
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("missing embedding for input index %d", i)
		}
	}
	return out, nil
}

func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage, temperature float64, maxTokens int) (string, error) {
	body := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode chat completion response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(payload.Error.Message) != "" {
			return "", errors.New(payload.Error.Message)
		}
		return "", fmt.Errorf("chat completion failed: %s", resp.Status)
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("chat completion returned no choices")
	}
	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}

func (c *Client) doJSON(ctx context.Context, method string, path string, body any) (*http.Response, error) {
	var rawBody []byte
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rawBody = payload
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.httpClient.Do(req)
}

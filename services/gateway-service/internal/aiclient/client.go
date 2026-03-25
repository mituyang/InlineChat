package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type endpointResolver interface {
	Resolve(ctx context.Context, serviceName string, protocol string) (string, error)
}

type Client struct {
	resolver       endpointResolver
	serviceName    string
	requestTimeout time.Duration
	httpClient     *http.Client
}

type ReloadResponse struct {
	SiteID     string `json:"site_id"`
	ReloadedAt string `json:"reloaded_at"`
	ChunkCount int    `json:"chunk_count"`
}

func NewDynamic(resolver endpointResolver, serviceName string, requestTimeout time.Duration) (*Client, error) {
	if resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil, fmt.Errorf("service_name is required")
	}
	if requestTimeout <= 0 {
		requestTimeout = 8 * time.Second
	}
	return &Client{
		resolver:       resolver,
		serviceName:    serviceName,
		requestTimeout: requestTimeout,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}, nil
}

func (c *Client) Reload(ctx context.Context, siteID string) (*ReloadResponse, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}

	target, err := c.resolveTarget(ctx)
	if err != nil {
		return nil, err
	}
	reqURL, err := buildReloadURL(target, siteID)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body struct {
		Error string `json:"error"`
		ReloadResponse
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode ai reload response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(body.Error) != "" {
			return nil, errors.New(body.Error)
		}
		return nil, fmt.Errorf("ai reload failed: %s", resp.Status)
	}

	return &body.ReloadResponse, nil
}

func (c *Client) resolveTarget(ctx context.Context) (string, error) {
	resolveCtx := ctx
	cancel := func() {}
	if c.requestTimeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
	}
	target, err := c.resolver.Resolve(resolveCtx, c.serviceName, "http")
	cancel()
	if err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("resolved empty ai-service target")
	}
	return target, nil
}

func buildReloadURL(target string, siteID string) (string, error) {
	baseURL, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid ai-service target: %w", err)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/reload"
	query := baseURL.Query()
	query.Set("site_id", siteID)
	baseURL.RawQuery = query.Encode()
	return baseURL.String(), nil
}

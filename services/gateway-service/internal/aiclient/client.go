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

type SiteStatus struct {
	SiteID         string `json:"site_id"`
	KnowledgeDir   string `json:"knowledge_dir"`
	IndexStatus    string `json:"index_status"`
	IndexedChunks  int    `json:"indexed_chunks"`
	LastIndexedAt  string `json:"last_indexed_at"`
	LastIndexError string `json:"last_index_error"`
	ActiveJobID    string `json:"active_job_id"`
}

type ReindexResponse struct {
	SiteID string `json:"site_id"`
	JobID  string `json:"job_id"`
	Status string `json:"status"`
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

func (c *Client) GetSiteStatus(ctx context.Context, siteID string) (*SiteStatus, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}
	target, err := c.resolveTarget(ctx)
	if err != nil {
		return nil, err
	}
	reqURL, err := buildSiteURL(target, siteID, "/status")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
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
		SiteStatus
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode ai status response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(body.Error) != "" {
			return nil, errors.New(body.Error)
		}
		return nil, fmt.Errorf("ai status failed: %s", resp.Status)
	}

	return &body.SiteStatus, nil
}

func (c *Client) StartReindex(ctx context.Context, siteID string) (*ReindexResponse, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}

	target, err := c.resolveTarget(ctx)
	if err != nil {
		return nil, err
	}
	reqURL, err := buildSiteURL(target, siteID, "/reindex")
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
		ReindexResponse
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode ai reindex response failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if strings.TrimSpace(body.Error) != "" {
			return nil, errors.New(body.Error)
		}
		return nil, fmt.Errorf("ai reindex failed: %s", resp.Status)
	}
	return &body.ReindexResponse, nil
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

func buildSiteURL(target string, siteID string, suffix string) (string, error) {
	baseURL, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid ai-service target: %w", err)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/sites/" + url.PathEscape(siteID) + suffix
	baseURL.RawQuery = ""
	return baseURL.String(), nil
}

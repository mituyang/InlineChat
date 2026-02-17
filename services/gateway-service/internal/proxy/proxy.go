package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

func NewReverseProxy(targetRawURL string, stripPrefix string, requestIDHeader string, logger *zap.Logger) (http.Handler, error) {
	target, err := url.Parse(targetRawURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalHost := req.Host
		originalProto := forwardedProto(req)
		authHeader := req.Header.Get("Authorization")
		requestID := req.Header.Get(requestIDHeader)

		originalDirector(req)

		trimmed := strings.TrimPrefix(req.URL.Path, stripPrefix)
		if trimmed == "" {
			trimmed = "/"
		}
		if !strings.HasPrefix(trimmed, "/") {
			trimmed = "/" + trimmed
		}

		req.URL.Path = joinURLPath(target.Path, trimmed)
		req.Host = target.Host

		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if requestID != "" && requestIDHeader != "" {
			req.Header.Set(requestIDHeader, requestID)
		}
		if originalHost != "" {
			req.Header.Set("X-Forwarded-Host", originalHost)
		}
		if originalProto != "" {
			req.Header.Set("X-Forwarded-Proto", originalProto)
		}
	}
	rp.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		requestID := strings.TrimSpace(req.Header.Get(requestIDHeader))
		logger.Error("upstream request failed",
			zap.Error(err),
			zap.String("target", targetRawURL),
			zap.String("request_id", requestID),
			zap.String("path", req.URL.Path),
		)
		writeErrorResponse(w, http.StatusBadGateway, requestID, "upstream_unavailable", "upstream service unavailable")
	}

	return rp, nil
}

func joinURLPath(base string, path string) string {
	if strings.HasSuffix(base, "/") {
		return strings.TrimSuffix(base, "/") + path
	}
	return base + path
}

func forwardedProto(req *http.Request) string {
	if v := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); v != "" {
		return v
	}
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

func writeErrorResponse(w http.ResponseWriter, status int, requestID string, code string, message string) {
	type errorResponse struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}

	payload := errorResponse{
		RequestID: requestID,
	}
	payload.Error.Code = code
	payload.Error.Message = message

	w.Header().Set("Content-Type", "application/json")
	if requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

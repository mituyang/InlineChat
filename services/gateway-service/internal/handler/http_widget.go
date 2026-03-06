package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
)

const (
	widgetSessionHeader  = "X-InlineChat-Widget-Session"
	widgetSessionVersion = "ws1"
	widgetSessionTTL     = 12 * time.Hour
)

type widgetSessionClaims struct {
	SiteID       string `json:"site_id"`
	SiteDomain   string `json:"site_domain"`
	ParentOrigin string `json:"parent_origin"`
	StorageScope string `json:"storage_scope"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

func (h *HTTPHandler) ServeWidgetApp(c *gin.Context) {
	if len(h.widgetIndexHTML) == 0 {
		c.String(http.StatusServiceUnavailable, "widget app unavailable")
		return
	}

	siteID := strings.TrimSpace(c.Query("site_id"))
	if siteID == "" {
		c.String(http.StatusBadRequest, "site_id is required")
		return
	}

	site, err := h.fetchSiteBySiteID(c, siteID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			c.String(http.StatusBadRequest, "invalid site_id")
			return
		}
		handleGRPCError(c, err)
		return
	}
	if strings.TrimSpace(strings.ToLower(site.GetStatus())) != "active" {
		c.String(http.StatusConflict, "site is not active")
		return
	}

	parentOrigin, err := validateWidgetRequestSource(site.GetDomain(), c.Query("parent_origin"), c.GetHeader("Referer"), c.GetHeader("Origin"))
	if err != nil {
		c.String(http.StatusForbidden, err.Error())
		return
	}

	sessionToken, claims, err := h.issueWidgetSession(site, parentOrigin)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to initialize widget")
		return
	}

	page, err := buildWidgetIndexHTML(h.widgetIndexHTML, sessionToken, claims.StorageScope)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to initialize widget")
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, page)
}

func (h *HTTPHandler) requireWidgetSession(c *gin.Context, site *adminv1.Site) error {
	token := strings.TrimSpace(c.GetHeader(widgetSessionHeader))
	if token == "" {
		abortForbidden(c, "widget session is required")
		return status.Error(codes.PermissionDenied, "widget session is required")
	}

	claims, err := validateWidgetSessionToken(token, site.GetWidgetKey(), h.now())
	if err != nil {
		abortForbidden(c, "invalid widget session")
		return status.Error(codes.PermissionDenied, "invalid widget session")
	}
	if claims.SiteID != strings.TrimSpace(site.GetSiteId()) {
		abortForbidden(c, "invalid widget session")
		return status.Error(codes.PermissionDenied, "invalid widget session")
	}
	if claims.SiteDomain != normalizeHostLike(site.GetDomain()) {
		abortForbidden(c, "invalid widget session")
		return status.Error(codes.PermissionDenied, "invalid widget session")
	}
	return nil
}

func (h *HTTPHandler) issueWidgetSession(site *adminv1.Site, parentOrigin string) (string, widgetSessionClaims, error) {
	now := h.now().UTC()
	claims := widgetSessionClaims{
		SiteID:       strings.TrimSpace(site.GetSiteId()),
		SiteDomain:   normalizeHostLike(site.GetDomain()),
		ParentOrigin: normalizeOrigin(parentOrigin),
		StorageScope: buildWidgetStorageScope(strings.TrimSpace(site.GetSiteId()), site.GetDomain()),
		IssuedAt:     now.Unix(),
		ExpiresAt:    now.Add(widgetSessionTTL).Unix(),
	}
	token, err := signWidgetSessionToken(claims, site.GetWidgetKey())
	if err != nil {
		return "", widgetSessionClaims{}, err
	}
	return token, claims, nil
}

func validateWidgetRequestSource(siteDomain string, parentOrigin string, referer string, origin string) (string, error) {
	normalizedParentOrigin := normalizeOrigin(parentOrigin)
	if normalizedParentOrigin == "" {
		return "", fmt.Errorf("parent_origin is required")
	}
	if !matchesSiteDomain(siteDomain, normalizedParentOrigin) {
		return "", fmt.Errorf("parent origin is not allowed")
	}

	for _, candidate := range []string{referer, origin} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if matchesSiteDomain(siteDomain, candidate) {
			return normalizedParentOrigin, nil
		}
		return "", fmt.Errorf("widget source is not allowed")
	}

	return "", fmt.Errorf("widget source is missing")
}

func buildWidgetIndexHTML(indexHTML []byte, sessionToken string, storageScope string) (string, error) {
	tokenJSON, err := json.Marshal(strings.TrimSpace(sessionToken))
	if err != nil {
		return "", err
	}
	scopeJSON, err := json.Marshal(strings.TrimSpace(storageScope))
	if err != nil {
		return "", err
	}

	bootstrap := fmt.Sprintf(
		`<script>window.__INLINECHAT_WIDGET_SESSION__=%s;window.__INLINECHAT_WIDGET_SCOPE__=%s;</script>`,
		string(tokenJSON),
		string(scopeJSON),
	)

	page := string(indexHTML)
	target := `<script src="/app/widget/app.js" defer></script>`
	if strings.Contains(page, target) {
		return strings.Replace(page, target, bootstrap+"\n    "+target, 1), nil
	}
	if strings.Contains(page, "</body>") {
		return strings.Replace(page, "</body>", "    "+bootstrap+"\n  </body>", 1), nil
	}
	return page + bootstrap, nil
}

func signWidgetSessionToken(claims widgetSessionClaims, widgetKey string) (string, error) {
	key := strings.TrimSpace(widgetKey)
	if key == "" {
		return "", fmt.Errorf("widget_key is required")
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := widgetSessionVersion + "." + payloadEncoded
	signature := signWidgetSessionMAC(unsigned, key)
	return unsigned + "." + signature, nil
}

func validateWidgetSessionToken(raw string, widgetKey string, now time.Time) (*widgetSessionClaims, error) {
	key := strings.TrimSpace(widgetKey)
	if key == "" {
		return nil, fmt.Errorf("widget_key is required")
	}

	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid widget session")
	}
	if parts[0] != widgetSessionVersion {
		return nil, fmt.Errorf("invalid widget session")
	}

	unsigned := parts[0] + "." + parts[1]
	expected := signWidgetSessionMAC(unsigned, key)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, fmt.Errorf("invalid widget session")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid widget session")
	}

	var claims widgetSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid widget session")
	}
	if strings.TrimSpace(claims.SiteID) == "" || strings.TrimSpace(claims.SiteDomain) == "" {
		return nil, fmt.Errorf("invalid widget session")
	}
	if claims.ExpiresAt <= 0 || now.UTC().Unix() > claims.ExpiresAt {
		return nil, fmt.Errorf("widget session expired")
	}
	return &claims, nil
}

func signWidgetSessionMAC(unsigned string, widgetKey string) string {
	mac := hmac.New(sha256.New, []byte(widgetKey))
	_, _ = mac.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func buildWidgetStorageScope(siteID string, siteDomain string) string {
	normalizedSiteID := strings.TrimSpace(siteID)
	normalizedDomain := normalizeHostLike(siteDomain)
	if normalizedDomain == "" {
		return normalizedSiteID
	}
	if normalizedSiteID == "" {
		return normalizedDomain
	}
	return normalizedSiteID + "@" + normalizedDomain
}

func matchesSiteDomain(siteDomain string, raw string) bool {
	normalizedSiteDomain := normalizeHostLike(siteDomain)
	normalizedHost := normalizeHostLike(raw)
	return normalizedSiteDomain != "" && normalizedSiteDomain == normalizedHost
}

func normalizeOrigin(raw string) string {
	parsed, ok := parseURLLike(raw)
	if !ok {
		return ""
	}
	host := normalizeHostFromURL(parsed)
	if host == "" {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host
}

func normalizeHostLike(raw string) string {
	parsed, ok := parseURLLike(raw)
	if ok {
		return normalizeHostFromURL(parsed)
	}

	text := strings.TrimSpace(strings.ToLower(raw))
	text = strings.TrimPrefix(text, ".")
	text = strings.TrimSuffix(text, ".")
	if text == "" {
		return ""
	}

	hostURL, ok := parseURLLike("https://" + text)
	if !ok {
		return ""
	}
	return normalizeHostFromURL(hostURL)
}

func parseURLLike(raw string) (*url.URL, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, false
	}
	parsed, err := url.Parse(text)
	if err == nil && parsed.Host != "" {
		return parsed, true
	}
	return nil, false
}

func normalizeHostFromURL(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(parsed.Port())
	switch {
	case port == "":
		return host
	case strings.EqualFold(parsed.Scheme, "http") && port == "80":
		return host
	case strings.EqualFold(parsed.Scheme, "https") && port == "443":
		return host
	default:
		return host + ":" + port
	}
}

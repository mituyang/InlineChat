package httpmiddleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	defaultReferrerPolicy    = "strict-origin-when-cross-origin"
	defaultPermissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=()"
)

type SecurityHeadersOptions struct {
	ReferrerPolicy    string
	PermissionsPolicy string
}

func SecurityHeaders(opts SecurityHeadersOptions) gin.HandlerFunc {
	referrerPolicy := strings.TrimSpace(opts.ReferrerPolicy)
	if referrerPolicy == "" {
		referrerPolicy = defaultReferrerPolicy
	}
	permissionsPolicy := strings.TrimSpace(opts.PermissionsPolicy)
	if permissionsPolicy == "" {
		permissionsPolicy = defaultPermissionsPolicy
	}

	return func(c *gin.Context) {
		headers := c.Writer.Header()
		if headers.Get("X-Content-Type-Options") == "" {
			headers.Set("X-Content-Type-Options", "nosniff")
		}
		if headers.Get("Referrer-Policy") == "" {
			headers.Set("Referrer-Policy", referrerPolicy)
		}
		if headers.Get("Permissions-Policy") == "" {
			headers.Set("Permissions-Policy", permissionsPolicy)
		}
		c.Next()
	}
}

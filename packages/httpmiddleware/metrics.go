package httpmiddleware

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HTTPMetrics struct {
	requestTotal    *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	inflight        prometheus.Gauge
}

func NewHTTPMetrics(service string, reg prometheus.Registerer) *HTTPMetrics {
	normalizedService := sanitizeMetricName(service)
	if normalizedService == "" {
		normalizedService = "inlinechat_service"
	}
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &HTTPMetrics{
		requestTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "inlinechat",
			Subsystem: normalizedService,
			Name:      "http_requests_total",
			Help:      "Total count of HTTP requests by method/route/status.",
		}, []string{"method", "route", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "inlinechat",
			Subsystem: normalizedService,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency distribution by method/route/status.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		}, []string{"method", "route", "status"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "inlinechat",
			Subsystem: normalizedService,
			Name:      "http_inflight_requests",
			Help:      "Number of in-flight HTTP requests.",
		}),
	}

	reg.MustRegister(m.requestTotal, m.requestDuration, m.inflight)
	return m
}

func (m *HTTPMetrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		m.inflight.Inc()
		defer m.inflight.Dec()

		c.Next()

		method := strings.ToUpper(strings.TrimSpace(c.Request.Method))
		if method == "" {
			method = "UNKNOWN"
		}
		route := strings.TrimSpace(c.FullPath())
		if route == "" {
			route = "UNMATCHED"
		}
		status := strconv.Itoa(c.Writer.Status())
		m.requestTotal.WithLabelValues(method, route, status).Inc()
		m.requestDuration.WithLabelValues(method, route, status).Observe(time.Since(start).Seconds())
	}
}

func MetricsHandler(gatherer prometheus.Gatherer) gin.HandlerFunc {
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})
	return gin.WrapH(handler)
}

func sanitizeMetricName(v string) string {
	text := strings.TrimSpace(strings.ToLower(v))
	if text == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		ch := text[i]
		isLetter := ch >= 'a' && ch <= 'z'
		isDigit := ch >= '0' && ch <= '9'
		if isLetter || isDigit {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('_')
	}

	out := strings.Trim(b.String(), "_")
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "svc_" + out
	}
	return out
}

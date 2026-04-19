package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseHAConnections(t *testing.T) {
	metrics := `
# HELP cloudflared_tunnel_ha_connections Number of active ha connections
# TYPE cloudflared_tunnel_ha_connections gauge
cloudflared_tunnel_ha_connections 2
`

	value, err := parseHAConnections(strings.NewReader(metrics))
	if err != nil {
		t.Fatalf("parseHAConnections returned error: %v", err)
	}
	if value != 2 {
		t.Fatalf("expected 2, got %v", value)
	}
}

func TestShouldRestartOn1033AndZeroConnections(t *testing.T) {
	cfg := config{
		minHAConnections: 1,
		publicHealthURL:  "https://example.com/readyz",
	}
	result := probeResult{
		publicConfigured: true,
		haConnections:    0,
		metricsHealthy:   true,
		publicHealthy:    false,
		publicIs1033:     true,
		publicStatus:     530,
	}

	restart, _ := shouldRestart(cfg, result)
	if !restart {
		t.Fatal("expected restart to be required")
	}
}

func TestShouldNotRestartWhenPublicStillHealthy(t *testing.T) {
	cfg := config{
		minHAConnections: 1,
		publicHealthURL:  "https://example.com/readyz",
	}
	result := probeResult{
		publicConfigured: true,
		haConnections:    0,
		metricsHealthy:   true,
		publicHealthy:    true,
		publicStatus:     200,
	}

	restart, _ := shouldRestart(cfg, result)
	if restart {
		t.Fatal("expected restart to be skipped while public health is still good")
	}
}

func TestParseLabelFilters(t *testing.T) {
	labels := parseLabelFilters("com.docker.compose.project=inlinechat,com.docker.compose.service=cloudflared")
	if labels["com.docker.compose.project"] != "inlinechat" {
		t.Fatalf("unexpected project label: %#v", labels)
	}
	if labels["com.docker.compose.service"] != "cloudflared" {
		t.Fatalf("unexpected service label: %#v", labels)
	}
}

func TestParseDurationEnvFallback(t *testing.T) {
	value, err := parseDurationEnv("WATCHDOG_DURATION_TEST", 30*time.Second)
	if err != nil {
		t.Fatalf("parseDurationEnv returned error: %v", err)
	}
	if value != 30*time.Second {
		t.Fatalf("expected fallback duration, got %s", value)
	}
}

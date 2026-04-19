package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type config struct {
	dockerSocket          string
	metricsURL            string
	publicHealthURL       string
	expectStatus          int
	checkInterval         time.Duration
	failThreshold         int
	restartCooldown       time.Duration
	metricsTimeout        time.Duration
	publicTimeout         time.Duration
	minHAConnections      float64
	targetContainerName   string
	targetContainerLabels map[string]string
}

type probeResult struct {
	haConnections    float64
	metricsHealthy   bool
	metricsErr       error
	publicConfigured bool
	publicHealthy    bool
	publicStatus     int
	publicErr        error
	publicBody       string
	publicIs1033     bool
}

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("加载 cloudflared watchdog 配置失败: %v", err)
	}

	log.Printf("cloudflared watchdog 已启动: metrics=%s public=%s interval=%s threshold=%d cooldown=%s",
		cfg.metricsURL, emptyAs(cfg.publicHealthURL, "disabled"), cfg.checkInterval, cfg.failThreshold, cfg.restartCooldown)

	publicClient := &http.Client{Timeout: cfg.publicTimeout}
	metricsClient := &http.Client{Timeout: cfg.metricsTimeout}
	dockerClient := newDockerClient(cfg.dockerSocket)

	ticker := time.NewTicker(cfg.checkInterval)
	defer ticker.Stop()

	var consecutiveFailures int
	var lastRestart time.Time

	run := func() {
		result := probe(cfg, publicClient, metricsClient)
		restart, reason := shouldRestart(cfg, result)
		if !restart {
			if consecutiveFailures > 0 {
				log.Printf("cloudflared 健康检查恢复: ha=%.0f public_status=%d", result.haConnections, result.publicStatus)
			}
			consecutiveFailures = 0
			return
		}

		consecutiveFailures++
		log.Printf("cloudflared 健康检查失败(%d/%d): %s", consecutiveFailures, cfg.failThreshold, reason)

		if consecutiveFailures < cfg.failThreshold {
			return
		}
		if !lastRestart.IsZero() && time.Since(lastRestart) < cfg.restartCooldown {
			log.Printf("cloudflared 重启冷却中: 还需等待 %s", cfg.restartCooldown-time.Since(lastRestart))
			return
		}

		target, err := resolveContainerTarget(context.Background(), dockerClient, cfg)
		if err != nil {
			log.Printf("解析 cloudflared 容器失败: %v", err)
			return
		}

		if err := restartContainer(context.Background(), dockerClient, target); err != nil {
			log.Printf("重启 cloudflared 容器失败: %v", err)
			return
		}

		lastRestart = time.Now()
		consecutiveFailures = 0
		log.Printf("已触发 cloudflared 自动重启: target=%s reason=%s", target, reason)
	}

	run()
	for range ticker.C {
		run()
	}
}

func loadConfig() (config, error) {
	cfg := config{
		dockerSocket:          getenv("WATCHDOG_DOCKER_SOCKET", "/var/run/docker.sock"),
		metricsURL:            getenv("WATCHDOG_METRICS_URL", "http://cloudflared:20241/metrics"),
		publicHealthURL:       strings.TrimSpace(os.Getenv("WATCHDOG_PUBLIC_HEALTH_URL")),
		targetContainerName:   strings.TrimSpace(os.Getenv("WATCHDOG_TARGET_CONTAINER_NAME")),
		targetContainerLabels: parseLabelFilters(os.Getenv("WATCHDOG_TARGET_CONTAINER_LABELS")),
	}

	var err error
	if cfg.expectStatus, err = parseIntEnv("WATCHDOG_EXPECT_STATUS", 200); err != nil {
		return config{}, err
	}
	if cfg.failThreshold, err = parseIntEnv("WATCHDOG_FAIL_THRESHOLD", 3); err != nil {
		return config{}, err
	}
	if cfg.failThreshold < 1 {
		return config{}, errors.New("WATCHDOG_FAIL_THRESHOLD 必须 >= 1")
	}
	if cfg.checkInterval, err = parseDurationEnv("WATCHDOG_CHECK_INTERVAL", 30*time.Second); err != nil {
		return config{}, err
	}
	if cfg.restartCooldown, err = parseDurationEnv("WATCHDOG_RESTART_COOLDOWN", 2*time.Minute); err != nil {
		return config{}, err
	}
	if cfg.metricsTimeout, err = parseDurationEnv("WATCHDOG_METRICS_TIMEOUT", 5*time.Second); err != nil {
		return config{}, err
	}
	if cfg.publicTimeout, err = parseDurationEnv("WATCHDOG_PUBLIC_TIMEOUT", 8*time.Second); err != nil {
		return config{}, err
	}
	if cfg.minHAConnections, err = parseFloatEnv("WATCHDOG_MIN_HA_CONNECTIONS", 1); err != nil {
		return config{}, err
	}
	if cfg.targetContainerName == "" && len(cfg.targetContainerLabels) == 0 {
		return config{}, errors.New("WATCHDOG_TARGET_CONTAINER_NAME 或 WATCHDOG_TARGET_CONTAINER_LABELS 至少配置一个")
	}

	return cfg, nil
}

func probe(cfg config, publicClient, metricsClient *http.Client) probeResult {
	result := probeResult{
		publicConfigured: cfg.publicHealthURL != "",
	}

	haConnections, err := fetchHAConnections(metricsClient, cfg.metricsURL)
	if err != nil {
		result.metricsErr = err
	} else {
		result.metricsHealthy = true
		result.haConnections = haConnections
	}

	if !result.publicConfigured {
		return result
	}

	status, body, is1033, err := fetchPublicHealth(publicClient, cfg.publicHealthURL, cfg.expectStatus)
	result.publicStatus = status
	result.publicBody = body
	result.publicIs1033 = is1033
	if err != nil {
		result.publicErr = err
		return result
	}
	result.publicHealthy = true
	return result
}

func shouldRestart(cfg config, result probeResult) (bool, string) {
	metricsBad := !result.metricsHealthy || result.haConnections < cfg.minHAConnections
	if !metricsBad {
		return false, ""
	}

	if !result.publicConfigured {
		if result.metricsErr != nil {
			return true, fmt.Sprintf("metrics 不可用: %v", result.metricsErr)
		}
		return true, fmt.Sprintf("ha connections 过低: %.0f < %.0f", result.haConnections, cfg.minHAConnections)
	}

	if result.publicHealthy {
		return false, ""
	}

	if result.publicIs1033 {
		return true, fmt.Sprintf("公网探测命中 1033，ha=%.0f", result.haConnections)
	}
	if result.publicErr != nil && result.metricsErr != nil {
		return true, fmt.Sprintf("metrics 与公网探测同时失败: metrics=%v public=%v", result.metricsErr, result.publicErr)
	}
	if result.publicErr != nil {
		return true, fmt.Sprintf("公网探测失败且 ha=%.0f: %v", result.haConnections, result.publicErr)
	}
	return true, fmt.Sprintf("公网探测状态异常(status=%d)且 ha=%.0f", result.publicStatus, result.haConnections)
}

func fetchHAConnections(client *http.Client, metricsURL string) (float64, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, metricsURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metrics HTTP %d", resp.StatusCode)
	}

	return parseHAConnections(resp.Body)
}

func parseHAConnections(r io.Reader) (float64, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[0] != "cloudflared_tunnel_ha_connections" {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, fmt.Errorf("解析 ha connections 失败: %w", err)
		}
		return value, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("metrics 中缺少 cloudflared_tunnel_ha_connections")
}

func fetchPublicHealth(client *http.Client, healthURL string, expectStatus int) (int, string, bool, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
	if err != nil {
		return 0, "", false, err
	}
	req.Header.Set("User-Agent", "inlinechat-cloudflared-watchdog/1.0")
	req.Header.Set("Accept", "text/plain,text/html,application/json;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", false, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	body := compactSnippet(string(bodyBytes))
	is1033 := strings.Contains(strings.ToLower(body), "error 1033") ||
		strings.Contains(strings.ToLower(body), "cloudflare tunnel error")

	if resp.StatusCode != expectStatus {
		return resp.StatusCode, body, is1033, fmt.Errorf("health HTTP %d", resp.StatusCode)
	}

	return resp.StatusCode, body, is1033, nil
}

func newDockerClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func resolveContainerTarget(ctx context.Context, client *http.Client, cfg config) (string, error) {
	if cfg.targetContainerName != "" {
		return cfg.targetContainerName, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json?all=1", nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Docker API /containers/json 返回 %d", resp.StatusCode)
	}

	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return "", err
	}

	matches := make([]dockerContainer, 0, len(containers))
	for _, container := range containers {
		if matchesLabels(container.Labels, cfg.targetContainerLabels) {
			matches = append(matches, container)
		}
	}

	if len(matches) == 0 {
		return "", errors.New("未找到匹配的 cloudflared 容器")
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].State == matches[j].State {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].State == "running"
	})

	return matches[0].ID, nil
}

func restartContainer(ctx context.Context, client *http.Client, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/containers/"+url.PathEscape(target)+"/restart?t=10", nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("Docker API restart 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func matchesLabels(actual, expected map[string]string) bool {
	for key, expectedValue := range expected {
		if actual[key] != expectedValue {
			return false
		}
	}
	return true
}

func parseLabelFilters(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	labels := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		labels[key] = value
	}
	return labels
}

func parseIntEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是有效整数: %w", name, err)
	}
	return value, nil
}

func parseFloatEnv(name string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s 不是有效数字: %w", name, err)
	}
	return value, nil
}

func parseDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是有效时长: %w", name, err)
	}
	return value, nil
}

func getenv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func compactSnippet(value string) string {
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	value = strings.Join(strings.Fields(replacer.Replace(value)), " ")
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
}

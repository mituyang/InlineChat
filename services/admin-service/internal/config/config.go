package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort                     string
	GRPCPort                     string
	LogLevel                     string
	MySQLDSN                     string
	JWTSecret                    string
	JWTPreviousSecret            string
	JWTIssuer                    string
	BCryptCost                   int
	ETCDEndpoints                []string
	ETCDDialTimeoutSec           int
	ETCDRegisterTTLSec           int
	DiscoveryPrefix              string
	ServiceName                  string
	ServiceInstanceID            string
	ServiceAdvertiseGRPCEndpoint string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:                     getEnv("HTTP_PORT", "8204"),
		GRPCPort:                     getEnv("GRPC_PORT", "8214"),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		MySQLDSN:                     os.Getenv("MYSQL_DSN"),
		JWTSecret:                    os.Getenv("JWT_SECRET"),
		JWTPreviousSecret:            strings.TrimSpace(os.Getenv("JWT_PREVIOUS_SECRET")),
		JWTIssuer:                    getEnv("JWT_ISSUER", "inlinechat-auth"),
		BCryptCost:                   getIntEnv("BCRYPT_COST", 12),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		ETCDRegisterTTLSec:           getIntEnv("ETCD_REGISTER_TTL_SEC", 15),
		DiscoveryPrefix:              getEnv("DISCOVERY_PREFIX", "/inlinechat/services"),
		ServiceName:                  getEnv("SERVICE_NAME", "admin-service"),
		ServiceInstanceID:            strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID")),
		ServiceAdvertiseGRPCEndpoint: os.Getenv("SERVICE_ADVERTISE_GRPC_ENDPOINT"),
	}

	if cfg.MySQLDSN == "" {
		return Config{}, fmt.Errorf("MYSQL_DSN is required")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.BCryptCost < 10 || cfg.BCryptCost > 14 {
		return Config{}, fmt.Errorf("BCRYPT_COST must be between 10 and 14")
	}
	if len(cfg.ETCDEndpoints) == 0 {
		return Config{}, fmt.Errorf("ETCD_ENDPOINTS is required")
	}
	if cfg.ETCDDialTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("ETCD_DIAL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.ETCDRegisterTTLSec <= 0 {
		return Config{}, fmt.Errorf("ETCD_REGISTER_TTL_SEC must be greater than 0")
	}
	if cfg.ServiceName == "" {
		return Config{}, fmt.Errorf("SERVICE_NAME is required")
	}
	if strings.TrimSpace(cfg.ServiceAdvertiseGRPCEndpoint) == "" {
		return Config{}, fmt.Errorf("SERVICE_ADVERTISE_GRPC_ENDPOINT is required")
	}

	return cfg, nil
}

func getEnv(key string, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func getIntEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

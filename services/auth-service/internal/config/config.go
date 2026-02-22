package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPPort                     string
	GRPCPort                     string
	LogLevel                     string
	MySQLDSN                     string
	MySQLMaxOpenConns            int
	MySQLMaxIdleConns            int
	MySQLConnMaxLifetimeSec      int
	MySQLConnMaxIdleTimeSec      int
	MySQLQueryTimeoutMS          int
	JWTSecret                    string
	JWTPreviousSecret            string
	JWTIssuer                    string
	JWTExpire                    time.Duration
	SuperAdminEmail              string
	SuperAdminPassword           string
	SuperAdminDisplayName        string
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
	expire, err := time.ParseDuration(getEnv("JWT_EXPIRE", "12h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid JWT_EXPIRE: %w", err)
	}

	cfg := Config{
		HTTPPort:                     getEnv("HTTP_PORT", "8201"),
		GRPCPort:                     getEnv("GRPC_PORT", "8211"),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		MySQLDSN:                     os.Getenv("MYSQL_DSN"),
		MySQLMaxOpenConns:            getIntEnv("MYSQL_MAX_OPEN_CONNS", 80),
		MySQLMaxIdleConns:            getIntEnv("MYSQL_MAX_IDLE_CONNS", 20),
		MySQLConnMaxLifetimeSec:      getIntEnv("MYSQL_CONN_MAX_LIFETIME_SEC", 900),
		MySQLConnMaxIdleTimeSec:      getIntEnv("MYSQL_CONN_MAX_IDLE_TIME_SEC", 300),
		MySQLQueryTimeoutMS:          getIntEnv("MYSQL_QUERY_TIMEOUT_MS", 1500),
		JWTSecret:                    os.Getenv("JWT_SECRET"),
		JWTPreviousSecret:            strings.TrimSpace(os.Getenv("JWT_PREVIOUS_SECRET")),
		JWTIssuer:                    getEnv("JWT_ISSUER", "inlinechat-auth"),
		JWTExpire:                    expire,
		SuperAdminEmail:              os.Getenv("SUPER_ADMIN_EMAIL"),
		SuperAdminPassword:           os.Getenv("SUPER_ADMIN_PASSWORD"),
		SuperAdminDisplayName:        os.Getenv("SUPER_ADMIN_DISPLAY_NAME"),
		BCryptCost:                   getIntEnv("BCRYPT_COST", 12),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		ETCDRegisterTTLSec:           getIntEnv("ETCD_REGISTER_TTL_SEC", 15),
		DiscoveryPrefix:              getEnv("DISCOVERY_PREFIX", "/inlinechat/services"),
		ServiceName:                  getEnv("SERVICE_NAME", "auth-service"),
		ServiceInstanceID:            strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID")),
		ServiceAdvertiseGRPCEndpoint: os.Getenv("SERVICE_ADVERTISE_GRPC_ENDPOINT"),
	}

	if cfg.MySQLDSN == "" {
		return Config{}, fmt.Errorf("MYSQL_DSN is required")
	}
	if cfg.MySQLMaxOpenConns <= 0 {
		return Config{}, fmt.Errorf("MYSQL_MAX_OPEN_CONNS must be greater than 0")
	}
	if cfg.MySQLMaxIdleConns <= 0 {
		return Config{}, fmt.Errorf("MYSQL_MAX_IDLE_CONNS must be greater than 0")
	}
	if cfg.MySQLMaxIdleConns > cfg.MySQLMaxOpenConns {
		return Config{}, fmt.Errorf("MYSQL_MAX_IDLE_CONNS must be less than or equal to MYSQL_MAX_OPEN_CONNS")
	}
	if cfg.MySQLConnMaxLifetimeSec <= 0 {
		return Config{}, fmt.Errorf("MYSQL_CONN_MAX_LIFETIME_SEC must be greater than 0")
	}
	if cfg.MySQLConnMaxIdleTimeSec <= 0 {
		return Config{}, fmt.Errorf("MYSQL_CONN_MAX_IDLE_TIME_SEC must be greater than 0")
	}
	if cfg.MySQLQueryTimeoutMS <= 0 {
		return Config{}, fmt.Errorf("MYSQL_QUERY_TIMEOUT_MS must be greater than 0")
	}
	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if strings.TrimSpace(cfg.SuperAdminEmail) == "" {
		return Config{}, fmt.Errorf("SUPER_ADMIN_EMAIL is required")
	}
	if strings.TrimSpace(cfg.SuperAdminPassword) == "" {
		return Config{}, fmt.Errorf("SUPER_ADMIN_PASSWORD is required")
	}
	if strings.TrimSpace(cfg.SuperAdminDisplayName) == "" {
		return Config{}, fmt.Errorf("SUPER_ADMIN_DISPLAY_NAME is required")
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

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
	MySQLDSN                     string
	LogLevel                     string
	AutoCloseAfterSec            int
	RedisAddr                    string
	RedisPassword                string
	RedisDB                      int
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
		HTTPPort:                     getEnv("HTTP_PORT", "8202"),
		GRPCPort:                     getEnv("GRPC_PORT", "8212"),
		MySQLDSN:                     os.Getenv("MYSQL_DSN"),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		AutoCloseAfterSec:            getIntEnv("AUTO_CLOSE_AFTER_SEC", 300),
		RedisAddr:                    os.Getenv("REDIS_ADDR"),
		RedisPassword:                os.Getenv("REDIS_PASSWORD"),
		RedisDB:                      getIntEnv("REDIS_DB", 0),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		ETCDRegisterTTLSec:           getIntEnv("ETCD_REGISTER_TTL_SEC", 15),
		DiscoveryPrefix:              getEnv("DISCOVERY_PREFIX", "/inlinechat/services"),
		ServiceName:                  getEnv("SERVICE_NAME", "chat-service"),
		ServiceInstanceID:            strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID")),
		ServiceAdvertiseGRPCEndpoint: os.Getenv("SERVICE_ADVERTISE_GRPC_ENDPOINT"),
	}

	if cfg.MySQLDSN == "" {
		return Config{}, fmt.Errorf("MYSQL_DSN is required")
	}
	if cfg.RedisAddr == "" {
		return Config{}, fmt.Errorf("REDIS_ADDR is required")
	}
	if cfg.AutoCloseAfterSec <= 0 {
		return Config{}, fmt.Errorf("AUTO_CLOSE_AFTER_SEC must be greater than 0")
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

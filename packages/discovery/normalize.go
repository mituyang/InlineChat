package discovery

import "strings"

// normalizeEndpoints 过滤空 endpoint，避免 etcd 客户端接收无效地址。
func normalizeEndpoints(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(item)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// normalizePrefix 统一前缀格式，确保拼接路径稳定。
func normalizePrefix(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/inlinechat/services"
	}
	value = "/" + strings.Trim(value, "/")
	return value
}

package conf

import (
	"fmt"
	"strings"
)

type ModelConfig struct {
	Chat map[string]*ChatConfig
}

type ChatConfig struct {
	URL       string
	AccessKey string
	Model     string `json:",optional"`
}

// OpenAPIUpstreamByName 解析 core-api 认证后的开放接口上游名。
// 未配置、名称为空或配置不完整都必须失败，不能回退到默认 DeepSeek Key。
func (c *Config) OpenAPIUpstreamByName(name string) (*OpenAPIUpstream, error) {
	if c == nil || c.OpenAPI == nil {
		return nil, fmt.Errorf("openapi upstream configuration is unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("openapi upstream is required")
	}
	upstream, ok := c.OpenAPI.Upstreams[name]
	if !ok || upstream == nil {
		return nil, fmt.Errorf("openapi upstream %q is unavailable", name)
	}
	if upstream.URL == "" || upstream.Model == "" || upstream.AccessKey == "" {
		return nil, fmt.Errorf("openapi upstream %q is incomplete", name)
	}
	return upstream, nil
}

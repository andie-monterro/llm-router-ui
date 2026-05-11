package gateway

import (
	"github.com/michaelquigley/df/dd"
	"github.com/openziti/llm-gateway/routing"
)

type Config struct {
	Listen    string
	Zrok      *ZrokConfig
	Agora     *AgoraConfig
	Providers *ProvidersConfig
	Routing   *routing.RoutingConfig
	Metrics   *MetricsConfig
	APIKeys   *APIKeysConfig
	Tracing   *TracingConfig
}

type TracingConfig struct {
	Enabled          bool
	MaxContentLength int // max characters per message content (default: 200)
}

type ZrokConfig struct {
	Share *ZrokShareConfig
}

type ZrokShareConfig struct {
	Enabled bool
	Mode    string // public or private (default: private)
	Token   string // existing persistent share token (private shares only)
}

type AgoraConfig struct {
	Enabled         bool
	IntegrationFile string

	APIEndpoint string
	EnvRoot     string

	InstanceName string
	Description  string
	TunnelMode   string // tcp, http, or udp

	Advertisement *AgoraAdvertisementConfig
	Serve         *AgoraServeConfig
}

type AgoraAdvertisementConfig struct {
	Publish      *bool
	WorkgroupIDs []string `dd:"workgroup_ids"`
	ContractID   string
	Capabilities []string
}

type AgoraServeConfig struct {
	Enabled       bool
	BackendTarget string
	Grants        []string
}

type AgoraIntegrationFile struct {
	APIEndpoint   string
	EnvRoot       string
	Advertisement *AgoraIntegrationAdvertisement
}

type AgoraIntegrationAdvertisement struct {
	WorkgroupIDs []string `dd:"workgroup_ids"`
	ContractID   string
}

type ProvidersConfig struct {
	OpenAI    *OpenAIConfig
	Anthropic *AnthropicConfig
	Local     *LocalConfig
}

type OpenAIConfig struct {
	APIKey         string
	BaseURL        string
	ZrokShareToken string
	AgoraService   string
}

type AnthropicConfig struct {
	APIKey         string
	BaseURL        string
	ZrokShareToken string
	AgoraService   string
}

type LocalConfig struct {
	BaseURL        string
	ZrokShareToken string
	AgoraService   string
	Endpoints      []LocalEndpointConfig
	HealthCheck    *HealthCheckConfig
}

type LocalEndpointConfig struct {
	Name           string
	BaseURL        string
	ZrokShareToken string
	AgoraService   string
	Weight         int
}

type HealthCheckConfig struct {
	IntervalSeconds int
	TimeoutSeconds  int
}

type MetricsConfig struct {
	Enabled bool
	Listen  string // address for metrics server (default: ":9090")
}

type APIKeysConfig struct {
	Enabled bool
	Keys    []APIKeyEntry
}

type APIKeyEntry struct {
	Name          string
	Key           string
	AllowedModels []string
	AllowedRoutes []string
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{}
	if err := dd.MergeYAMLFile(cfg, path); err != nil {
		return nil, err
	}
	return cfg, nil
}

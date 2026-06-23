package gateway

import (
	"fmt"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/openziti/llm-gateway/agora"
	"github.com/openziti/llm-gateway/routing"
)

type Config struct {
	Listen    string
	Zrok      *ZrokConfig
	Agora     *agora.Config
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

type ProvidersConfig struct {
	OpenAI    *OpenAIConfig
	Anthropic *AnthropicConfig
	Local     *LocalConfig
}

type OpenAIConfig struct {
	APIKey         string
	BaseURL        string
	ZrokShareToken string
	AgoraTunnel    string
}

type AnthropicConfig struct {
	APIKey         string
	BaseURL        string
	ZrokShareToken string
	AgoraTunnel    string
}

type LocalConfig struct {
	BaseURL        string
	ZrokShareToken string
	AgoraTunnel    string
	Endpoints      []LocalEndpointConfig
	HealthCheck    *HealthCheckConfig
}

type LocalEndpointConfig struct {
	Name           string
	BaseURL        string
	ZrokShareToken string
	AgoraTunnel    string
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
	// resolve agora env vars + integration file before any agora field is read.
	if err := agora.ResolveConfig(cfg.Agora); err != nil {
		return nil, err
	}
	if err := cfg.validateAgora(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// AgoraServeEnabled reports whether the gateway should serve its handler over
// an agora tunnel.
func (c *Config) AgoraServeEnabled() bool {
	return c != nil && c.Agora != nil && c.Agora.Enabled && agora.ServeEnabled(c.Agora)
}

// AgoraPublishEnabled reports whether the gateway should publish a catalog
// advertisement. Publishing requires serving in this iteration: a dial-only
// gateway never publishes an advertisement whose name points at a front-door
// tunnel it does not bind.
func (c *Config) AgoraPublishEnabled() bool {
	return c.AgoraServeEnabled() && agora.AdvertisementPublish(c.Agora)
}

// collectAgoraTunnels returns the unique, trimmed agora_tunnel names for only
// the providers and endpoints initProviders will actually initialize — no
// phantom attachments. OpenAI/Anthropic count only when configured (the same
// APIKey gate initProviders uses); Local contributes its endpoint tunnels in
// multi mode and its single tunnel only in single mode.
func collectAgoraTunnels(cfg *Config) []string {
	if cfg == nil || cfg.Providers == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var tunnels []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		tunnels = append(tunnels, name)
	}

	if p := cfg.Providers.OpenAI; p != nil && p.APIKey != "" {
		add(p.AgoraTunnel)
	}
	if p := cfg.Providers.Anthropic; p != nil && p.APIKey != "" {
		add(p.AgoraTunnel)
	}
	if l := cfg.Providers.Local; l != nil {
		if len(l.Endpoints) > 0 {
			for _, ep := range l.Endpoints {
				add(ep.AgoraTunnel)
			}
		} else {
			add(l.AgoraTunnel)
		}
	}
	return tunnels
}

// validateAgora enforces the fail-fast preconditions that keep per-site
// agora_tunnel values and agora.serve.enabled meaningful: each requires the
// agora subsystem, and an explicit publish request requires serving.
func (c *Config) validateAgora() error {
	// (a) dial side — a per-site agora_tunnel is meaningless without the subsystem.
	if len(collectAgoraTunnels(c)) > 0 && (c.Agora == nil || !c.Agora.Enabled) {
		return fmt.Errorf("agora_tunnel set on a provider/endpoint requires agora.enabled: true")
	}
	// (b) serve side (symmetric) — serve.enabled without enabled would silently
	// fall back to the plaintext local listener.
	if c.Agora != nil && c.Agora.Serve != nil && c.Agora.Serve.Enabled && !c.Agora.Enabled {
		return fmt.Errorf("agora.serve.enabled requires agora.enabled: true")
	}
	// (c) explicit publish without serve — honor the request loudly, not silently.
	if agora.PublishExplicit(c.Agora) && !c.AgoraServeEnabled() {
		return fmt.Errorf("agora.advertisement.publish requires agora.serve.enabled in this iteration")
	}
	return nil
}

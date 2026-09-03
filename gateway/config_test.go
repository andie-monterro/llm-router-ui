package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openziti/llm-gateway/keys"
	"github.com/openziti/llm-gateway/routing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigStrictAPIKeys(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
		secret  string
	}{
		{
			name: "valid nullable restrictions",
			config: `api_keys:
  enabled: true
  keys:
    - name: alice
      key: sk-gw-alice
      allowed_models: null
      allowed_routes: null
`,
		},
		{
			name: "unknown block field",
			config: `api_keys:
  enabled: true
  kyas: []
`,
			wantErr: "api_keys: unknown field 'kyas'",
		},
		{
			name: "unknown record field",
			config: `api_keys:
  enabled: true
  keys:
    - name: alice
      key: sk-gw-alice
      allowed_model: gpt-*
`,
			wantErr: "api_keys.keys[0]: unknown field 'allowed_model'",
		},
		{
			name: "coercible timestamp name",
			config: `api_keys:
  enabled: true
  keys:
    - name: 2026-01-01T00:00:00Z
      key: sk-gw-alice
`,
			wantErr: "api_keys.keys[0].name",
		},
		{
			name: "numeric key does not leak",
			config: `api_keys:
  enabled: true
  keys:
    - name: alice
      key: 12345678
`,
			wantErr: "api_keys.keys[0].key",
			secret:  "12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeTestConfig(t, tt.config))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadConfig() = %v, want nil", err)
				}
				if cfg.APIKeys == nil || len(cfg.APIKeys.Keys) != 1 || cfg.APIKeys.Keys[0].AllowedModels != nil {
					t.Fatalf("strict key config = %#v", cfg.APIKeys)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig() = %v, want error containing %q", err, tt.wantErr)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Errorf("LoadConfig() error exposed key material: %v", err)
			}
		})
	}
}

func TestNewRejectsConfigKeyOutsideBearerGrammar(t *testing.T) {
	cfg, err := LoadConfig(writeTestConfig(t, `api_keys:
  enabled: true
  keys:
    - name: alice
      key: "not valid"
`))
	if err != nil {
		t.Fatalf("LoadConfig() = %v, want nil before store mapping", err)
	}
	_, err = New(cfg)
	if err == nil || !strings.Contains(err.Error(), "api_keys.keys[0].key") {
		t.Fatalf("New() = %v, want bearer-grammar error", err)
	}
	if strings.Contains(err.Error(), "not valid") {
		t.Error("New() error exposed key material")
	}
}

func TestLoadConfigFileKeySource(t *testing.T) {
	cfg, err := LoadConfig(writeTestConfig(t, `api_keys:
  enabled: true
  sources:
    - type: file
      path: /tmp/keys.yaml
      watch: true
`))
	if err != nil {
		t.Fatalf("LoadConfig() = %v, want nil", err)
	}
	if cfg.APIKeys == nil || len(cfg.APIKeys.Sources) != 1 {
		t.Fatalf("api key config = %#v", cfg.APIKeys)
	}
	source, ok := cfg.APIKeys.Sources[0].(*keys.FileSourceConfig)
	if !ok || source.Name != "file[0]" || source.PollInterval != 30*time.Second || !source.Watch {
		t.Fatalf("file source = %#v", cfg.APIKeys.Sources[0])
	}
}

func TestLoadConfigHTTPKeySource(t *testing.T) {
	t.Setenv("LLMGW_KEY_API", "https://keys.internal")
	t.Setenv("LLMGW_KEY_API_TOKEN", "management-token")
	cfg, err := LoadConfig(writeTestConfig(t, `api_keys:
  enabled: true
  sources:
    - type: http
      base_url: ${LLMGW_KEY_API}
      token: ${LLMGW_KEY_API_TOKEN}
`))
	if err != nil {
		t.Fatalf("LoadConfig() = %v, want nil", err)
	}
	if cfg.APIKeys == nil || len(cfg.APIKeys.Sources) != 1 {
		t.Fatalf("api key config = %#v", cfg.APIKeys)
	}
	source, ok := cfg.APIKeys.Sources[0].(*keys.HTTPSourceConfig)
	if !ok || source.Name != "http[0]" || source.BaseURL != "https://keys.internal" ||
		source.Token != "management-token" || source.PollInterval != 30*time.Second || source.Timeout != 5*time.Second {
		t.Fatalf("HTTP source = %#v", cfg.APIKeys.Sources[0])
	}
}

func TestLoadConfigRejectsUnsetHTTPKeySourceValue(t *testing.T) {
	_, err := LoadConfig(writeTestConfig(t, `api_keys:
  enabled: true
  sources:
    - type: http
      base_url: https://keys.internal
      token: ${LLMGW_UNSET_KEY_API_TOKEN}
`))
	if err == nil || !strings.Contains(err.Error(), "api_keys.sources[0].token resolves empty") ||
		strings.Contains(err.Error(), "LLMGW_UNSET_KEY_API_TOKEN") {
		t.Fatalf("LoadConfig() = %v, want safe token expansion error", err)
	}
}

func TestLoadConfigRejectsInvalidFileKeySource(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			"disabled sources",
			`api_keys:
  enabled: false
  sources:
    - type: file
      path: /tmp/keys.yaml
`,
			"requires api_keys.enabled",
		},
		{
			"zero poll interval",
			`api_keys:
  enabled: true
  sources:
    - type: file
      path: /tmp/keys.yaml
      poll_interval: 0s
`,
			"poll_interval must be positive",
		},
		{
			"bare poll interval",
			`api_keys:
  enabled: true
  sources:
    - type: file
      path: /tmp/keys.yaml
      poll_interval: 30
`,
			"expected duration string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeTestConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewBootLoadsFileKeySource(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "keys.yaml")
	if err := os.WriteFile(keyPath, []byte("version: 1\nkeys:\n  - name: alice\n    key: sk-alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeTestConfig(t, `api_keys:
  enabled: true
  sources:
    - type: file
      name: managed
      path: `+keyPath+`
`)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	t.Cleanup(gateway.cleanup)
	if record, ok := gateway.keyStore.Lookup("sk-alice"); !ok || record.Name != "alice" || record.Source != "managed" {
		t.Fatalf("file key lookup = %+v, %v", record, ok)
	}
}

func TestExpandEnvSecrets(t *testing.T) {
	t.Setenv("LLMGW_TEST_KEY", "sk-resolved")

	// set variables resolve into the config.
	cfg := &Config{
		Providers: &ProvidersConfig{
			OpenAI: &OpenAIConfig{APIKey: "${LLMGW_TEST_KEY}"},
		},
		APIKeys: &keys.Config{
			Enabled: true,
			Keys:    []keys.EntryConfig{{Name: "alice", Key: "${LLMGW_TEST_KEY}"}},
		},
	}
	if err := keys.ResolveConfig(cfg.APIKeys); err != nil {
		t.Fatalf("keys.ResolveConfig() = %v, want nil", err)
	}
	if err := cfg.expandEnv(); err != nil {
		t.Fatalf("expandEnv() = %v, want nil", err)
	}
	if cfg.Providers.OpenAI.APIKey != "sk-resolved" {
		t.Errorf("provider key = %q, want resolved value", cfg.Providers.OpenAI.APIKey)
	}
	if cfg.APIKeys.Keys[0].Key != "sk-resolved" {
		t.Errorf("virtual key = %q, want resolved value", cfg.APIKeys.Keys[0].Key)
	}

	// a placeholder that resolves empty is a directed error, not silence.
	cfg = &Config{Providers: &ProvidersConfig{
		Anthropic: &AnthropicConfig{APIKey: "${LLMGW_TEST_UNSET_VAR}"},
	}}
	if err := cfg.expandEnv(); err == nil || !strings.Contains(err.Error(), "providers.anthropic.api_key") {
		t.Fatalf("expandEnv() = %v, want resolves-empty error", err)
	}

	// an enabled key entry with an empty key is a directed error.
	cfg = &Config{APIKeys: &keys.Config{
		Enabled: true,
		Keys:    []keys.EntryConfig{{Name: "bob"}},
	}}
	if err := keys.ResolveConfig(cfg.APIKeys); err == nil || !strings.Contains(err.Error(), "empty key") {
		t.Fatalf("keys.ResolveConfig() = %v, want empty-key error", err)
	}

	// values left empty stay "not configured"; disabled key blocks are inert.
	cfg = &Config{
		Providers: &ProvidersConfig{OpenAI: &OpenAIConfig{}},
		APIKeys:   &keys.Config{Keys: []keys.EntryConfig{{Name: "carol"}}},
	}
	if err := keys.ResolveConfig(cfg.APIKeys); err != nil {
		t.Fatalf("keys.ResolveConfig() = %v, want nil for disabled keys", err)
	}
	if err := cfg.expandEnv(); err != nil {
		t.Fatalf("expandEnv() = %v, want nil for empty/disabled", err)
	}

	// local base URLs expand like the cloud providers do — single and endpoint.
	t.Setenv("LLMGW_TEST_HOST", "http://ollama.internal:11434")
	cfg = &Config{Providers: &ProvidersConfig{Local: &LocalConfig{
		BaseURL:   "${LLMGW_TEST_HOST}",
		Endpoints: []LocalEndpointConfig{{Name: "a", BaseURL: "${LLMGW_TEST_HOST}"}},
	}}}
	if err := cfg.expandEnv(); err != nil {
		t.Fatalf("expandEnv() = %v, want nil", err)
	}
	if cfg.Providers.Local.BaseURL != "http://ollama.internal:11434" {
		t.Errorf("local base_url = %q, want resolved value", cfg.Providers.Local.BaseURL)
	}
	if cfg.Providers.Local.Endpoints[0].BaseURL != "http://ollama.internal:11434" {
		t.Errorf("endpoint base_url = %q, want resolved value", cfg.Providers.Local.Endpoints[0].BaseURL)
	}

	// OMP and other launchers can bind routing models from their own config.
	t.Setenv("LLMGW_TEST_MODEL", "openai-codex/gpt-5.6-luna")
	cfg = &Config{Routing: &routing.RoutingConfig{
		Classifier: &routing.ClassifierConfig{Model: "${LLMGW_TEST_MODEL}"},
		Routes:     []routing.RouteConfig{{Name: "fast", Model: "${LLMGW_TEST_MODEL}"}},
	}}
	if err := cfg.expandEnv(); err != nil {
		t.Fatalf("expandEnv() = %v, want nil", err)
	}
	if cfg.Routing.Classifier.Model != "openai-codex/gpt-5.6-luna" || cfg.Routing.Routes[0].Model != "openai-codex/gpt-5.6-luna" {
		t.Fatalf("routing models were not expanded: %+v", cfg.Routing)
	}
}

func TestResolveEmbedProviderOpenAIInheritsOverlayClient(t *testing.T) {
	overlay := &http.Client{}
	g := &Gateway{
		cfg: &Config{Providers: &ProvidersConfig{
			OpenAI: &OpenAIConfig{APIKey: "sk-test"},
		}},
		openaiHTTPClient: overlay,
	}

	baseURL, apiKey, httpClient, err := g.resolveRoutingProvider("openai")
	if err != nil {
		t.Fatalf("resolveRoutingProvider() = %v, want nil", err)
	}
	if httpClient != overlay {
		t.Errorf("openai embed client must be the provider's overlay-backed client")
	}
	if baseURL != "https://api.openai.com" {
		t.Errorf("base URL = %q, want the unchanged default", baseURL)
	}
	if apiKey != "sk-test" {
		t.Errorf("api key = %q, want configured key", apiKey)
	}
}

func TestResolveEmbedProviderRefusesUnusable(t *testing.T) {
	// a keyless openai block selected as a routing provider is a directed
	// error, not a runtime 401.
	g := &Gateway{cfg: &Config{Providers: &ProvidersConfig{OpenAI: &OpenAIConfig{}}}}
	if _, _, _, err := g.resolveRoutingProvider("openai"); err == nil ||
		!strings.Contains(err.Error(), "requires providers.open_ai.api_key") {
		t.Fatalf("resolveRoutingProvider() = %v, want keyless-openai error", err)
	}

	// an unknown provider name is refused, not reported as unconfigured.
	g = &Gateway{cfg: &Config{Providers: &ProvidersConfig{}}}
	if _, _, _, err := g.resolveRoutingProvider("ollama"); err == nil ||
		!strings.Contains(err.Error(), "unknown routing provider 'ollama'") {
		t.Fatalf("resolveRoutingProvider() = %v, want unknown-provider error", err)
	}
}

func TestValidateProvidersOverlayRequiresAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "openai agora tunnel without key",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{AgoraTunnel: "egress"},
			}},
			wantErr: "providers.open_ai.agora_tunnel",
		},
		{
			name: "anthropic zrok token without key",
			cfg: &Config{Providers: &ProvidersConfig{
				Anthropic: &AnthropicConfig{ZrokShareToken: "abc123"},
			}},
			wantErr: "providers.anthropic.zrok_share_token",
		},
		{
			name: "openai agora tunnel with key",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{APIKey: "sk-test", AgoraTunnel: "egress"},
			}},
		},
		{
			name: "local overlay needs no key",
			cfg: &Config{Providers: &ProvidersConfig{
				Local: &LocalConfig{AgoraTunnel: "infer"},
			}},
		},
		{
			name: "top-level local agora tunnel with endpoints is refused",
			cfg: &Config{Providers: &ProvidersConfig{
				Local: &LocalConfig{
					AgoraTunnel: "infer",
					Endpoints:   []LocalEndpointConfig{{Name: "a", BaseURL: "http://x"}},
				},
			}},
			wantErr: "providers.local.agora_tunnel is ignored in multi-endpoint mode",
		},
		{
			name: "top-level local zrok token with endpoints is refused",
			cfg: &Config{Providers: &ProvidersConfig{
				Local: &LocalConfig{
					ZrokShareToken: "abc123",
					Endpoints:      []LocalEndpointConfig{{Name: "a", BaseURL: "http://x"}},
				},
			}},
			wantErr: "providers.local.zrok_share_token is ignored in multi-endpoint mode",
		},
		{
			name: "cloud providers without overlays are not validated here",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI:    &OpenAIConfig{},
				Anthropic: &AnthropicConfig{},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validateProviders()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateProviders() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateProviders() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// a written base_url the gateway cannot dial is configuration it cannot honor;
// deferring it to the request path starts the gateway healthy but dark. the key
// subsystem already enforces this on a source base_url.
func TestValidateProvidersBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "openai missing scheme",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{APIKey: "sk-test", BaseURL: "api.openai.com"},
			}},
			wantErr: "providers.open_ai.base_url",
		},
		{
			name: "anthropic unsupported scheme",
			cfg: &Config{Providers: &ProvidersConfig{
				Anthropic: &AnthropicConfig{APIKey: "sk-test", BaseURL: "ftp://api.anthropic.com"},
			}},
			wantErr: "providers.anthropic.base_url",
		},
		{
			name: "local missing host",
			cfg: &Config{Providers: &ProvidersConfig{
				Local: &LocalConfig{BaseURL: "http://"},
			}},
			wantErr: "providers.local.base_url",
		},
		{
			name: "endpoint names its index",
			cfg: &Config{Providers: &ProvidersConfig{
				Local: &LocalConfig{Endpoints: []LocalEndpointConfig{
					{Name: "a", BaseURL: "http://10.0.0.1:11434"},
					{Name: "b", BaseURL: "10.0.0.2:11434"},
				}},
			}},
			wantErr: "providers.local.endpoints[1].base_url",
		},
		{
			name: "empty base_url is not configured",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{APIKey: "sk-test"},
				Local:  &LocalConfig{},
			}},
		},
		{
			name: "http and https both accepted",
			cfg: &Config{Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{APIKey: "sk-test", BaseURL: "https://api.openai.com"},
				Local:  &LocalConfig{BaseURL: "http://localhost:11434"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validateProviders()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateProviders() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateProviders() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

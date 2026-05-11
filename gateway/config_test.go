package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigAgoraBlock(t *testing.T) {
	path := writeTempConfig(t, `
listen: ":9090"
agora:
  enabled: true
  integration_file: "/tmp/agora.yaml"
  api_endpoint: "http://127.0.0.1:8080"
  env_root: "/tmp/agora"
  instance_name: "engineering"
  description: "engineering gateway"
  tunnel_mode: "tcp"
  advertisement:
    publish: false
    workgroup_ids:
      - wg_abcdefghijkl
    contract_id: con_abcdefghijkl
    capabilities:
      - llm-routing
  serve:
    enabled: true
    backend_target: "127.0.0.1:9090"
    grants:
      - alice@example.com
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.Agora == nil || !cfg.Agora.Enabled {
		t.Fatal("expected enabled agora config")
	}
	if cfg.Agora.APIEndpoint != "http://127.0.0.1:8080" {
		t.Fatalf("APIEndpoint = %q", cfg.Agora.APIEndpoint)
	}
	if cfg.Agora.Advertisement == nil || cfg.Agora.Advertisement.Publish == nil || *cfg.Agora.Advertisement.Publish {
		t.Fatalf("expected advertisement.publish=false, got %#v", cfg.Agora.Advertisement)
	}
	if got := cfg.Agora.Advertisement.WorkgroupIDs; len(got) != 1 || got[0] != "wg_abcdefghijkl" {
		t.Fatalf("unexpected workgroup IDs: %#v", got)
	}
	if cfg.Agora.Serve == nil || !cfg.Agora.Serve.Enabled || cfg.Agora.Serve.BackendTarget != "127.0.0.1:9090" {
		t.Fatalf("unexpected serve config: %#v", cfg.Agora.Serve)
	}
}

func TestLoadConfigProviderAgoraService(t *testing.T) {
	path := writeTempConfig(t, `
providers:
  open_ai:
    api_key: "sk-test"
    agora_service: "openai-relay"
  anthropic:
    api_key: "sk-ant-test"
    agora_service: "anthropic-relay"
  local:
    agora_service: "local-relay"
    endpoints:
      - name: gpu
        agora_service: "gpu-relay"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.Providers.OpenAI.AgoraService != "openai-relay" {
		t.Fatalf("open_ai agora_service = %q", cfg.Providers.OpenAI.AgoraService)
	}
	if cfg.Providers.Anthropic.AgoraService != "anthropic-relay" {
		t.Fatalf("anthropic agora_service = %q", cfg.Providers.Anthropic.AgoraService)
	}
	if cfg.Providers.Local.AgoraService != "local-relay" {
		t.Fatalf("local agora_service = %q", cfg.Providers.Local.AgoraService)
	}
	if got := cfg.Providers.Local.Endpoints[0].AgoraService; got != "gpu-relay" {
		t.Fatalf("endpoint agora_service = %q", got)
	}
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

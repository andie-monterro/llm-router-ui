package gateway

import (
	"reflect"
	"testing"

	"github.com/openziti/llm-gateway/routing"
)

func TestDeriveAgoraCapabilities(t *testing.T) {
	t.Setenv("OPENAI_TEST_KEY", "sk-test")
	cfg := &Config{
		Agora: &AgoraConfig{Serve: &AgoraServeConfig{Enabled: true}},
		Providers: &ProvidersConfig{
			OpenAI:    &OpenAIConfig{APIKey: "${OPENAI_TEST_KEY}"},
			Anthropic: &AnthropicConfig{APIKey: "sk-ant-test"},
			Local:     &LocalConfig{},
		},
		Routing: &routing.RoutingConfig{
			Semantic: &routing.SemanticConfig{Enabled: true},
		},
	}

	got := deriveAgoraCapabilities(cfg)
	want := []string{"llm-routing", "openai", "anthropic", "local", "semantic-routing", "agora-serve"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
}

func TestDeriveAgoraCapabilitiesAlwaysIncludesRouting(t *testing.T) {
	got := deriveAgoraCapabilities(&Config{})
	want := []string{"llm-routing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
}

package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openziti/agora/sdk/agent/tunnel"
	"github.com/openziti/llm-gateway/providers"
)

func TestInitOpenAIProviderViaAgoraConnect(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		hits.Add(1)
		writeOpenAICompatChatResponse(w, "gpt-test")
	}))
	defer server.Close()

	cfg := &Config{
		Providers: &ProvidersConfig{
			OpenAI: &OpenAIConfig{
				APIKey:       "sk-test",
				AgoraService: "openai-relay",
			},
		},
	}
	g := newGatewayWithAgoraConnects(cfg, map[string]string{
		"openai": hostPort(server.URL),
	})

	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders returned error: %v", err)
	}

	resp, err := g.providers[providers.ProviderOpenAI].ChatCompletion(context.Background(), chatRequest("gpt-test"))
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if resp.Model != "gpt-test" {
		t.Fatalf("model = %q", resp.Model)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one agora-backed request, got %d", hits.Load())
	}
	if got := cfg.Providers.OpenAI.BaseURL; got != "http://"+hostPort(server.URL) {
		t.Fatalf("base url = %q", got)
	}
}

func TestInitAnthropicProviderViaAgoraConnect(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	cfg := &Config{
		Providers: &ProvidersConfig{
			Anthropic: &AnthropicConfig{
				APIKey:       "sk-ant-test",
				AgoraService: "anthropic-relay",
			},
		},
	}
	g := newGatewayWithAgoraConnects(cfg, map[string]string{
		"anthropic": hostPort(server.URL),
	})

	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders returned error: %v", err)
	}

	resp, err := g.providers[providers.ProviderAnthropic].ChatCompletion(context.Background(), chatRequest("claude-test"))
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if resp.Choices[0].Message.GetContentString() != "ok" {
		t.Fatalf("content = %q", resp.Choices[0].Message.GetContentString())
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one agora-backed request, got %d", hits.Load())
	}
	if got := cfg.Providers.Anthropic.BaseURL; got != "http://"+hostPort(server.URL) {
		t.Fatalf("base url = %q", got)
	}
}

func TestInitLocalProviderViaAgoraConnect(t *testing.T) {
	var hits atomic.Int64
	server := newOpenAICompatServer(&hits)
	defer server.Close()

	cfg := &Config{
		Providers: &ProvidersConfig{
			Local: &LocalConfig{
				AgoraService: "local-relay",
			},
		},
	}
	g := newGatewayWithAgoraConnects(cfg, map[string]string{
		"local": hostPort(server.URL),
	})

	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders returned error: %v", err)
	}

	if _, err := g.providers[providers.ProviderLocal].ChatCompletion(context.Background(), chatRequest("llama3")); err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one agora-backed request, got %d", hits.Load())
	}
	if got := cfg.Providers.Local.BaseURL; got != "http://"+hostPort(server.URL) {
		t.Fatalf("base url = %q", got)
	}
}

func TestInitLocalMultiProviderWithMixedAgoraEndpoint(t *testing.T) {
	var directHits atomic.Int64
	direct := newOpenAICompatServer(&directHits)
	defer direct.Close()

	var agoraHits atomic.Int64
	agora := newOpenAICompatServer(&agoraHits)
	defer agora.Close()

	cfg := &Config{
		Providers: &ProvidersConfig{
			Local: &LocalConfig{
				Endpoints: []LocalEndpointConfig{
					{Name: "direct", BaseURL: direct.URL},
					{Name: "remote", AgoraService: "remote-relay"},
				},
				HealthCheck: &HealthCheckConfig{
					IntervalSeconds: 3600,
					TimeoutSeconds:  1,
				},
			},
		},
	}
	g := newGatewayWithAgoraConnects(cfg, map[string]string{
		"local:remote": hostPort(agora.URL),
	})

	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders returned error: %v", err)
	}
	defer closeProvider(t, g.providers[providers.ProviderLocal])

	if _, err := g.providers[providers.ProviderLocal].ChatCompletion(context.Background(), chatRequest("llama3")); err != nil {
		t.Fatalf("first ChatCompletion returned error: %v", err)
	}
	if _, err := g.providers[providers.ProviderLocal].ChatCompletion(context.Background(), chatRequest("llama3")); err != nil {
		t.Fatalf("second ChatCompletion returned error: %v", err)
	}

	if directHits.Load() == 0 {
		t.Fatal("direct endpoint was not used")
	}
	if agoraHits.Load() == 0 {
		t.Fatal("agora endpoint was not used")
	}
}

func newGatewayWithAgoraConnects(cfg *Config, addresses map[string]string) *Gateway {
	connects := map[string]*tunnel.ConnectStatus{}
	for key, address := range addresses {
		connects[key] = &tunnel.ConnectStatus{
			Name:          key,
			ListenAddress: address,
		}
	}
	return &Gateway{
		cfg:       cfg,
		providers: map[providers.ProviderType]providers.Provider{},
		agora: &agoraSubsystem{
			connects: connects,
		},
	}
}

func newOpenAICompatServer(hits *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			hits.Add(1)
			writeOpenAICompatChatResponse(w, "llama3")
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"llama3","object":"model","owned_by":"local"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeOpenAICompatChatResponse(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":"chatcmpl_test","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, model)
}

func chatRequest(model string) *providers.ChatCompletionRequest {
	return &providers.ChatCompletionRequest{
		Model: model,
		Messages: []providers.Message{
			{Role: "user", Content: "hello"},
		},
	}
}

func hostPort(rawURL string) string {
	return strings.TrimPrefix(rawURL, "http://")
}

func closeProvider(t *testing.T, p providers.Provider) {
	t.Helper()
	if c, ok := p.(io.Closer); ok {
		if err := c.Close(); err != nil {
			t.Fatalf("close provider: %v", err)
		}
	}
}

package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/openziti/llm-gateway/keys"
	"github.com/openziti/llm-gateway/providers"
)

type keySourceRoundTripper func(*http.Request) (*http.Response, error)

func (f keySourceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestKeySourceHTTPClientSelection(t *testing.T) {
	gateway := &Gateway{}
	direct, err := gateway.keySourceHTTPClient(&keys.HTTPSourceConfig{Name: "direct"})
	if err != nil || direct != http.DefaultClient {
		t.Fatalf("direct client = %p, %v, want http.DefaultClient", direct, err)
	}

	agoraClient := &http.Client{}
	var dialed string
	gateway.agoraDial = func(name string) (*http.Client, error) {
		dialed = name
		return agoraClient, nil
	}
	selected, err := gateway.keySourceHTTPClient(&keys.HTTPSourceConfig{
		Name: "managed", AgoraTunnel: " keys-egress ", ZrokShareToken: "zrok-token",
	})
	if err != nil || selected != agoraClient || dialed != "keys-egress" || len(gateway.accesses) != 0 {
		t.Fatalf("agora selection = %p, %v, dialed %q, accesses %d", selected, err, dialed, len(gateway.accesses))
	}

	zrokClient := &http.Client{}
	gateway.agoraDial = nil
	gateway.createAccess = func(label, token string) (*Access, error) {
		if label != "managed" {
			t.Fatalf("zrok access label = %q", label)
		}
		if token != "zrok-token" {
			t.Fatalf("zrok token = %q", token)
		}
		return &Access{httpClient: zrokClient}, nil
	}
	selected, err = gateway.keySourceHTTPClient(&keys.HTTPSourceConfig{
		Name: "managed", ZrokShareToken: "zrok-token",
	})
	if err != nil || selected != zrokClient || len(gateway.accesses) != 1 {
		t.Fatalf("zrok selection = %p, %v, accesses %d", selected, err, len(gateway.accesses))
	}
}

func TestZrokProviderInitializationDoesNotLogShareTokens(t *testing.T) {
	logs := captureGatewayLogs(t)
	client := &http.Client{Transport: keySourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    request,
		}, nil
	})}
	newGateway := func(cfg *Config, wantLabels map[string]string) *Gateway {
		gateway := &Gateway{
			cfg:       cfg,
			providers: make(map[providers.ProviderType]providers.Provider),
		}
		gateway.createAccess = func(label, token string) (*Access, error) {
			if wantToken, ok := wantLabels[label]; !ok || token != wantToken {
				t.Fatalf("NewAccess(%q, %q), want labels %#v", label, token, wantLabels)
			}
			return &Access{label: label, shareToken: token, httpClient: client}, nil
		}
		return gateway
	}

	providerTokens := map[string]string{
		"openai":    "openai-share-token-sentinel",
		"anthropic": "anthropic-share-token-sentinel",
		"local":     "local-share-token-sentinel",
	}
	gateway := newGateway(&Config{Providers: &ProvidersConfig{
		OpenAI:    &OpenAIConfig{APIKey: "sk-openai", ZrokShareToken: providerTokens["openai"]},
		Anthropic: &AnthropicConfig{APIKey: "sk-anthropic", ZrokShareToken: providerTokens["anthropic"]},
		Local:     &LocalConfig{BaseURL: "http://local.invalid", ZrokShareToken: providerTokens["local"]},
	}}, providerTokens)
	if err := gateway.initProviders(); err != nil {
		t.Fatal(err)
	}
	gateway.cleanup()

	endpointTokens := map[string]string{"gpu-one": "endpoint-share-token-sentinel"}
	gateway = newGateway(&Config{Providers: &ProvidersConfig{Local: &LocalConfig{Endpoints: []LocalEndpointConfig{
		{Name: "gpu-one", BaseURL: "http://gpu.invalid", ZrokShareToken: endpointTokens["gpu-one"]},
	}}}}, endpointTokens)
	if err := gateway.initProviders(); err != nil {
		t.Fatal(err)
	}
	gateway.cleanup()

	output := logs.String()
	for _, token := range []string{
		providerTokens["openai"], providerTokens["anthropic"], providerTokens["local"], endpointTokens["gpu-one"],
	} {
		if strings.Contains(output, token) {
			t.Fatalf("provider initialization logs exposed zrok token %q: %q", token, output)
		}
	}
}

func TestInitKeyStoreInjectsAgoraClient(t *testing.T) {
	var requestHost, requestPath, cacheControl string
	client := &http.Client{Transport: keySourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		requestHost = request.URL.Host
		requestPath = request.URL.Path
		cacheControl = request.Header.Get("Cache-Control")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"version":1,"count":1,"keys":[{"name":"alice","key":"sk-alice"}]}`,
			)),
			Request: request,
		}, nil
	})}
	gateway := &Gateway{
		cfg: &Config{APIKeys: &keys.Config{
			Enabled: true,
			Sources: []dd.Dynamic{&keys.HTTPSourceConfig{
				Name: "managed", BaseURL: "https://keys.internal/root", AgoraTunnel: "keys-egress",
				PollInterval: time.Hour, Timeout: time.Second,
			}},
		}},
		agoraDial: func(name string) (*http.Client, error) {
			if name != "keys-egress" {
				t.Fatalf("agora tunnel = %q", name)
			}
			return client, nil
		},
	}
	store, err := gateway.initKeyStore()
	if err != nil {
		t.Fatalf("initKeyStore() = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if record, ok := store.Lookup("sk-alice"); !ok || record.Name != "alice" {
		t.Fatalf("HTTP key lookup = %+v, %v", record, ok)
	}
	if requestHost != "keys.internal" || requestPath != "/root/v1/keys" || cacheControl != "no-cache" {
		t.Fatalf("request = host %q path %q cache %q", requestHost, requestPath, cacheControl)
	}
}

type cleanupOrderProvider struct {
	closed chan struct{}
}

func (*cleanupOrderProvider) ChatCompletion(context.Context, *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, nil
}

func (*cleanupOrderProvider) ChatCompletionStream(context.Context, *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, nil
}

func (*cleanupOrderProvider) ListModels(context.Context) ([]providers.Model, error) {
	return nil, nil
}

func (p *cleanupOrderProvider) Close() error {
	close(p.closed)
	return nil
}

func TestCleanupJoinsKeyRefreshBeforeClosingBorrowedTransportOwner(t *testing.T) {
	transportClosed := make(chan struct{})
	refreshStarted := make(chan struct{})
	var calls atomic.Int32
	client := &http.Client{Transport: keySourceRoundTripper(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"version":1,"count":1,"keys":[{"name":"alice","key":"sk-alice"}]}`,
				)),
				Request: request,
			}, nil
		}
		close(refreshStarted)
		<-request.Context().Done()
		select {
		case <-transportClosed:
			t.Error("borrowed transport owner closed before key refresh exited")
		default:
		}
		return nil, request.Context().Err()
	})}
	cfg := &keys.Config{Enabled: true, Sources: []dd.Dynamic{&keys.HTTPSourceConfig{
		Name: "managed", BaseURL: "https://keys.internal", PollInterval: time.Hour, Timeout: time.Minute,
	}}}
	store, err := keys.NewStoreFromConfig(cfg, func(*keys.HTTPSourceConfig) (*http.Client, error) {
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := &cleanupOrderProvider{closed: transportClosed}
	gateway := &Gateway{
		keyStore: store,
		providers: map[providers.ProviderType]providers.Provider{
			providers.ProviderOpenAI: owner,
		},
	}
	store.TriggerAll()
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	gateway.cleanup()
	select {
	case <-transportClosed:
	default:
		t.Fatal("transport owner was not closed")
	}
	store.TriggerAll()
	if got := calls.Load(); got != 2 {
		t.Fatalf("HTTP loads after cleanup = %d, want 2 total", got)
	}
}

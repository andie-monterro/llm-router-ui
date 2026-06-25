package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/openziti/llm-gateway/providers"
)

// TestProviderWiringAgoraBeatsZrok asserts that when a provider carries both an
// agora_tunnel and a zrok_share_token, the gateway wires it over agora: the
// agora branch runs (g.agoraDial is invoked for the tunnel) and the zrok path —
// which would call NewAccess and fail without a zrok environment — is skipped.
func TestProviderWiringAgoraBeatsZrok(t *testing.T) {
	var dialed []string
	sentinel := &http.Client{}
	g := &Gateway{
		cfg: &Config{
			Providers: &ProvidersConfig{
				OpenAI: &OpenAIConfig{APIKey: "sk", AgoraTunnel: "openai-egress", ZrokShareToken: "zrok-tok"},
			},
		},
		providers: map[providers.ProviderType]providers.Provider{},
		agoraDial: func(name string) (*http.Client, error) {
			dialed = append(dialed, name)
			return sentinel, nil
		},
	}
	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders error: %v", err)
	}
	if len(dialed) != 1 || dialed[0] != "openai-egress" {
		t.Fatalf("expected agora dial for 'openai-egress' (agora > zrok), got %#v", dialed)
	}
	if g.providers[providers.ProviderOpenAI] == nil {
		t.Fatal("openai provider was not built")
	}
}

// TestEndpointWiringAgoraBeatsZrok protects the per-endpoint invariant directly:
// a LocalEndpointConfig with both fields set uses the agora client for that
// endpoint.
func TestEndpointWiringAgoraBeatsZrok(t *testing.T) {
	var dialed []string
	sentinel := &http.Client{}
	g := &Gateway{
		cfg: &Config{
			Providers: &ProvidersConfig{
				Local: &LocalConfig{
					Endpoints: []LocalEndpointConfig{
						{Name: "ep1", BaseURL: "http://ep1.invalid", AgoraTunnel: "local-1", ZrokShareToken: "zrok-tok"},
					},
				},
			},
		},
		providers: map[providers.ProviderType]providers.Provider{},
		agoraDial: func(name string) (*http.Client, error) {
			dialed = append(dialed, name)
			return sentinel, nil
		},
	}
	if err := g.initProviders(); err != nil {
		t.Fatalf("initProviders error: %v", err)
	}
	defer g.cleanup() // stops the multi-local health checks
	if len(dialed) != 1 || dialed[0] != "local-1" {
		t.Fatalf("expected agora dial for 'local-1' (agora > zrok), got %#v", dialed)
	}
	if _, ok := g.providers[providers.ProviderLocal].(*providers.MultiLocal); !ok {
		t.Fatal("multi-endpoint local provider was not built")
	}
}

// TestAgoraClientReachesStubOverPipe is the integration check: a provider built
// with an agora-style dial client — whose Transport.DialContext ignores addr and
// returns one end of a net.Pipe — reaches a stub HTTP server bound to the other
// end and round-trips a chat completion.
func TestAgoraClientReachesStubOverPipe(t *testing.T) {
	stub := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(providers.ChatCompletionResponse{
			ID:    "cmpl-over-agora",
			Model: "test-model",
			Choices: []providers.Choice{{
				Index:   0,
				Message: &providers.Message{Role: "assistant", Content: "pong"},
			}},
		})
	})}

	ln := newPipeListener()
	go stub.Serve(ln)
	defer stub.Close()

	// mirror the agora dialer: ignore addr, route every request over the tunnel
	// (here, the in-memory pipe to the stub).
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return ln.dial(ctx)
		},
	}}

	// base URL host is cosmetic — the pipe ignores it.
	provider := providers.NewOpenAIWithClient("k", "http://llm-gateway-tunnel", client)
	resp, err := provider.ChatCompletion(context.Background(), &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion over pipe failed: %v", err)
	}
	if resp.ID != "cmpl-over-agora" {
		t.Fatalf("unexpected response id %q (request did not reach the stub over the pipe)", resp.ID)
	}
}

// pipeListener is an in-memory net.Listener whose connections are net.Pipe
// halves: dial() hands the server half to Accept and returns the client half.
type pipeListener struct {
	conns     chan net.Conn
	closeOnce sync.Once
	closed    chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	clientEnd, serverEnd := net.Pipe()
	select {
	case l.conns <- serverEnd:
		return clientEnd, nil
	case <-l.closed:
		_ = clientEnd.Close()
		_ = serverEnd.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = clientEnd.Close()
		_ = serverEnd.Close()
		return nil, ctx.Err()
	}
}

package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/openziti/llm-gateway/providers"
	"github.com/openziti/llm-gateway/routing"
)

type Gateway struct {
	cfg             *Config
	providers       map[providers.ProviderType]providers.Provider
	router          *providers.Router
	semanticRouter  *routing.SemanticRouter
	keyStore        *KeyStore
	share           *Share
	agora           *agoraSubsystem
	accesses        []*Access
	localHTTPClient *http.Client
	meters          *meters
	metricsHandler  http.Handler
}

func New(cfg *Config) (_ *Gateway, err error) {
	g := &Gateway{
		cfg:       cfg,
		providers: make(map[providers.ProviderType]providers.Provider),
	}
	defer func() {
		if err != nil {
			g.cleanup()
		}
	}()

	if err = resolveAgoraConfig(cfg); err != nil {
		return nil, err
	}

	if cfg.Agora != nil && cfg.Agora.Enabled {
		g.agora, err = newAgoraSubsystem(cfg)
		if err != nil {
			return nil, err
		}
		if err = g.agora.BootstrapConnects(context.Background()); err != nil {
			return nil, err
		}
	}

	if err = g.initProviders(); err != nil {
		return nil, err
	}

	g.router = providers.NewRouter(g.providers)

	if cfg.Metrics != nil && cfg.Metrics.Enabled {
		m, handler, err := initMetrics()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize metrics: %w", err)
		}
		g.meters = m
		g.metricsHandler = handler
		dl.Info("initialized opentelemetry metrics")
	}

	if cfg.APIKeys != nil && cfg.APIKeys.Enabled && len(cfg.APIKeys.Keys) > 0 {
		g.keyStore = NewKeyStore(cfg.APIKeys.Keys)
		dl.Infof("loaded %d API key(s)", len(cfg.APIKeys.Keys))
	}

	if cfg.Routing != nil {
		sr, err := g.initSemanticRouter(cfg.Routing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize semantic router: %w", err)
		}
		g.semanticRouter = sr
	}

	return g, nil
}

func (g *Gateway) initProviders() error {
	if g.cfg.Providers == nil {
		return nil
	}

	// initialize openai provider
	if g.cfg.Providers.OpenAI != nil && g.cfg.Providers.OpenAI.APIKey != "" {
		apiKey := os.ExpandEnv(g.cfg.Providers.OpenAI.APIKey)
		baseURL := os.ExpandEnv(g.cfg.Providers.OpenAI.BaseURL)

		if g.cfg.Providers.OpenAI.AgoraTunnel != "" {
			baseURL, ok := g.agoraBaseURL("openai")
			if !ok {
				return fmt.Errorf("agora connect address for openai provider was not initialized")
			}
			g.cfg.Providers.OpenAI.BaseURL = baseURL
			g.providers[providers.ProviderOpenAI] = providers.NewOpenAI(apiKey, baseURL)
			dl.Infof("initialized openai provider via agora tunnel '%s' at '%s'", g.cfg.Providers.OpenAI.AgoraTunnel, baseURL)
		} else if g.cfg.Providers.OpenAI.ZrokShareToken != "" {
			access, err := NewAccess(g.cfg.Providers.OpenAI.ZrokShareToken)
			if err != nil {
				return err
			}
			g.accesses = append(g.accesses, access)
			g.providers[providers.ProviderOpenAI] = providers.NewOpenAIWithClient(apiKey, baseURL, access.HTTPClient())
			dl.Infof("initialized openai provider via zrok share '%s'", g.cfg.Providers.OpenAI.ZrokShareToken)
		} else {
			g.providers[providers.ProviderOpenAI] = providers.NewOpenAI(apiKey, baseURL)
			if baseURL != "" {
				dl.Infof("initialized openai provider at '%s'", baseURL)
			} else {
				dl.Info("initialized openai provider")
			}
		}
	}

	// initialize anthropic provider
	if g.cfg.Providers.Anthropic != nil && g.cfg.Providers.Anthropic.APIKey != "" {
		apiKey := os.ExpandEnv(g.cfg.Providers.Anthropic.APIKey)
		baseURL := os.ExpandEnv(g.cfg.Providers.Anthropic.BaseURL)

		if g.cfg.Providers.Anthropic.AgoraTunnel != "" {
			baseURL, ok := g.agoraBaseURL("anthropic")
			if !ok {
				return fmt.Errorf("agora connect address for anthropic provider was not initialized")
			}
			g.cfg.Providers.Anthropic.BaseURL = baseURL
			g.providers[providers.ProviderAnthropic] = providers.NewAnthropic(apiKey, baseURL)
			dl.Infof("initialized anthropic provider via agora tunnel '%s' at '%s'", g.cfg.Providers.Anthropic.AgoraTunnel, baseURL)
		} else if g.cfg.Providers.Anthropic.ZrokShareToken != "" {
			access, err := NewAccess(g.cfg.Providers.Anthropic.ZrokShareToken)
			if err != nil {
				return err
			}
			g.accesses = append(g.accesses, access)
			g.providers[providers.ProviderAnthropic] = providers.NewAnthropicWithClient(apiKey, baseURL, access.HTTPClient())
			dl.Infof("initialized anthropic provider via zrok share '%s'", g.cfg.Providers.Anthropic.ZrokShareToken)
		} else {
			g.providers[providers.ProviderAnthropic] = providers.NewAnthropic(apiKey, baseURL)
			if baseURL != "" {
				dl.Infof("initialized anthropic provider at '%s'", baseURL)
			} else {
				dl.Info("initialized anthropic provider")
			}
		}
	}

	// initialize local provider
	if g.cfg.Providers.Local != nil {
		if len(g.cfg.Providers.Local.Endpoints) > 0 {
			if err := g.initLocalMulti(); err != nil {
				return err
			}
		} else {
			if err := g.initLocalSingle(); err != nil {
				return err
			}
		}
	}

	return nil
}

func (g *Gateway) agoraBaseURL(key string) (string, bool) {
	if g.agora == nil {
		return "", false
	}
	address, ok := g.agora.ConnectAddress(key)
	if !ok {
		return "", false
	}
	return "http://" + address, true
}

func (g *Gateway) initLocalSingle() error {
	cfg := g.cfg.Providers.Local
	if cfg.AgoraTunnel != "" {
		baseURL, ok := g.agoraBaseURL("local")
		if !ok {
			return fmt.Errorf("agora connect address for local provider was not initialized")
		}
		cfg.BaseURL = baseURL
		g.providers[providers.ProviderLocal] = providers.NewLocal(baseURL)
		dl.Infof("initialized local provider via agora tunnel '%s' at '%s'", cfg.AgoraTunnel, baseURL)
	} else if cfg.ZrokShareToken != "" {
		access, err := NewAccess(cfg.ZrokShareToken)
		if err != nil {
			return fmt.Errorf("failed to create zrok access for local provider: %w", err)
		}
		g.accesses = append(g.accesses, access)
		g.localHTTPClient = access.HTTPClient()
		g.providers[providers.ProviderLocal] = providers.NewLocalWithClient(cfg.BaseURL, g.localHTTPClient)
		dl.Infof("initialized local provider via zrok share '%s'", cfg.ZrokShareToken)
	} else {
		g.providers[providers.ProviderLocal] = providers.NewLocal(cfg.BaseURL)
		dl.Infof("initialized local provider at '%s'", cfg.BaseURL)
	}
	return nil
}

func (g *Gateway) initLocalMulti() error {
	cfg := g.cfg.Providers.Local
	opts := make([]providers.EndpointOption, 0, len(cfg.Endpoints))

	for _, ep := range cfg.Endpoints {
		opt := providers.EndpointOption{
			Name:    ep.Name,
			BaseURL: ep.BaseURL,
			Weight:  ep.Weight,
		}
		if ep.AgoraTunnel != "" {
			baseURL, ok := g.agoraBaseURL("local:" + ep.Name)
			if !ok {
				return fmt.Errorf("agora connect address for local endpoint '%s' was not initialized", ep.Name)
			}
			opt.BaseURL = baseURL
		} else if ep.ZrokShareToken != "" {
			access, err := NewAccess(ep.ZrokShareToken)
			if err != nil {
				return fmt.Errorf("failed to create zrok access for endpoint '%s': %w", ep.Name, err)
			}
			g.accesses = append(g.accesses, access)
			opt.HTTPClient = access.HTTPClient()
		}
		opts = append(opts, opt)
	}

	multi := providers.NewMultiLocal(opts)

	// start health checks
	interval := 60 * time.Second
	timeout := 30 * time.Second
	if cfg.HealthCheck != nil {
		if cfg.HealthCheck.IntervalSeconds > 0 {
			interval = time.Duration(cfg.HealthCheck.IntervalSeconds) * time.Second
		}
		if cfg.HealthCheck.TimeoutSeconds > 0 {
			timeout = time.Duration(cfg.HealthCheck.TimeoutSeconds) * time.Second
		}
	}
	multi.StartHealthChecks(interval, timeout)

	g.providers[providers.ProviderLocal] = multi

	for _, ep := range cfg.Endpoints {
		if ep.AgoraTunnel != "" {
			baseURL, _ := g.agoraBaseURL("local:" + ep.Name)
			dl.Infof("initialized local endpoint '%s' via agora tunnel '%s' at '%s'", ep.Name, ep.AgoraTunnel, baseURL)
		} else if ep.ZrokShareToken != "" {
			dl.Infof("initialized local endpoint '%s' via zrok share '%s'", ep.Name, ep.ZrokShareToken)
		} else {
			dl.Infof("initialized local endpoint '%s' at '%s'", ep.Name, ep.BaseURL)
		}
	}
	dl.Infof("initialized multi-endpoint local provider with %d endpoints", len(cfg.Endpoints))

	return nil
}

func (g *Gateway) Run() error {
	handler := g.newHandler()

	// setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer g.cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		dl.Info("received shutdown signal")
		cancel()
	}()

	errCh := make(chan error, 3)
	var servers []*http.Server

	localServer, agoraBackendTarget, err := g.startLocalServer(handler, errCh)
	if err != nil {
		return err
	}
	servers = append(servers, localServer)

	if g.cfg.Zrok != nil && g.cfg.Zrok.Share != nil && g.cfg.Zrok.Share.Enabled {
		zrokServer, err := g.startZrokServer(handler, errCh)
		if err != nil {
			shutdownServers(servers)
			return err
		}
		servers = append(servers, zrokServer)
	}

	if g.agora != nil {
		if err := g.agora.StartServing(ctx, agoraBackendTarget); err != nil {
			shutdownServers(servers)
			return err
		}
	}

	select {
	case <-ctx.Done():
		dl.Info("shutting down server")
		shutdownServers(servers)
		return nil
	case err := <-errCh:
		cancel()
		shutdownServers(servers)
		return err
	}
}

func (g *Gateway) startLocalServer(handler http.Handler, errCh chan<- error) (*http.Server, string, error) {
	addr := listenAddress(g.cfg)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}

	server := &http.Server{
		Handler: handler,
	}

	dl.Infof("listening on '%s'", listener.Addr().String())

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	return server, agoraBackendTarget(addr, listener.Addr().String()), nil
}

func (g *Gateway) startZrokServer(handler http.Handler, errCh chan<- error) (*http.Server, error) {
	var share *Share
	var err error

	if g.cfg.Zrok.Share.Token != "" {
		// use existing persistent share (private only)
		share, err = NewShareFromToken(g.cfg.Zrok.Share.Token)
	} else {
		share, err = NewShare(g.cfg.Zrok.Share.Mode)
	}

	if err != nil {
		return nil, err
	}
	g.share = share

	dl.Infof("serving via zrok share '%s'", share.Token())

	server := &http.Server{
		Handler: handler,
	}

	go func() {
		if err := server.Serve(share.Listener()); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	return server, nil
}

func shutdownServers(servers []*http.Server) {
	for _, server := range servers {
		if err := server.Shutdown(context.Background()); err != nil {
			dl.Errorf("error shutting down server: %v", err)
		}
	}
}

func agoraBackendTarget(configured, actual string) string {
	if configured == "" {
		configured = ":8080"
	}
	_, port, err := net.SplitHostPort(configured)
	if err == nil && port == "0" {
		return actual
	}
	return configured
}

func (g *Gateway) cleanup() {
	for _, p := range g.providers {
		if c, ok := p.(io.Closer); ok {
			c.Close()
		}
	}
	if g.share != nil {
		g.share.Close()
	}
	for _, access := range g.accesses {
		access.Close()
	}
	if g.agora != nil {
		g.agora.Close()
	}
}

func (g *Gateway) initSemanticRouter(cfg *routing.RoutingConfig) (*routing.SemanticRouter, error) {
	var embedClient routing.Embedder

	// initialize embedding client if semantic matching is enabled
	if cfg.Semantic != nil && cfg.Semantic.Enabled {
		baseURL, apiKey, httpClient := g.resolveEmbedProvider(cfg.Semantic.Provider)
		if baseURL == "" {
			return nil, fmt.Errorf("embedding provider '%s' not configured", cfg.Semantic.Provider)
		}
		if httpClient != nil {
			embedClient = routing.NewEmbedClientWithHTTPClient(cfg.Semantic.Provider, cfg.Semantic.Model, baseURL, apiKey, httpClient)
		} else {
			embedClient = routing.NewEmbedClient(cfg.Semantic.Provider, cfg.Semantic.Model, baseURL, apiKey)
		}
	}

	// resolve classifier provider connection details
	var classifierBaseURL, classifierAPIKey string
	var classifierHTTPClient *http.Client
	if cfg.Classifier != nil && cfg.Classifier.Enabled {
		classifierBaseURL, classifierAPIKey, classifierHTTPClient = g.resolveEmbedProvider(cfg.Classifier.Provider)
		if classifierBaseURL == "" {
			return nil, fmt.Errorf("classifier provider '%s' not configured", cfg.Classifier.Provider)
		}
	}

	ctx := context.Background()
	return routing.NewSemanticRouterWithClassifier(ctx, cfg, embedClient, classifierBaseURL, classifierAPIKey, classifierHTTPClient)
}

// resolveEmbedProvider looks up connection details from provider config.
func (g *Gateway) resolveEmbedProvider(provider string) (baseURL, apiKey string, httpClient *http.Client) {
	if g.cfg.Providers == nil {
		return "", "", nil
	}

	switch provider {
	case "local":
		if g.cfg.Providers.Local != nil {
			if multi, ok := g.providers[providers.ProviderLocal].(*providers.MultiLocal); ok {
				baseURL = multi.PrimaryBaseURL()
				httpClient = multi.RoundRobinClient()
			} else {
				baseURL = g.cfg.Providers.Local.BaseURL
				if baseURL == "" {
					baseURL = "http://localhost:11434"
				}
				httpClient = g.localHTTPClient
			}
		}
	case "openai":
		if g.cfg.Providers.OpenAI != nil {
			apiKey = os.ExpandEnv(g.cfg.Providers.OpenAI.APIKey)
			baseURL = os.ExpandEnv(g.cfg.Providers.OpenAI.BaseURL)
			if baseURL == "" {
				baseURL = "https://api.openai.com"
			}
		}
	}

	return baseURL, apiKey, httpClient
}

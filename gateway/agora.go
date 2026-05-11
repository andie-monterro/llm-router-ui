package gateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/openziti/agora/sdk/agent"
	"github.com/openziti/agora/sdk/agent/catalog"
	"github.com/openziti/agora/sdk/agent/tunnel"
)

const agoraCleanupTimeout = 5 * time.Second

var (
	agoraWorkgroupIDPattern = regexp.MustCompile(`^wg_[a-z0-9]{12}$`)
	agoraContractIDPattern  = regexp.MustCompile(`^con_[a-z0-9]{12}$`)
)

type agoraConnectTarget struct {
	Key     string
	Service string
}

type agoraOps interface {
	NewStandalone(agent.StandaloneOptions) (any, error)
	RootAPIEndpoint(any) (endpoint, source string)
	EnvironmentAPIEndpoint(any) (endpoint string, ok bool)
	StartRuntime(context.Context, any) error
	EnsureConnected(context.Context, any, tunnel.ConnectSpec) (*tunnel.ConnectStatus, error)
	RemoveConnect(context.Context, any, string, string) error
	EnsureServed(context.Context, any, tunnel.ServeSpec) (*tunnel.ServeStatus, error)
	RemoveServe(context.Context, any, string) error
	EnsurePublished(context.Context, any, catalog.PublishSpec) (*catalog.Advertisement, error)
	Retract(context.Context, any, string) error
	Close(context.Context, any) error
}

type defaultAgoraOps struct{}

func (defaultAgoraOps) NewStandalone(opts agent.StandaloneOptions) (any, error) {
	return agent.NewStandalone(opts)
}

func (defaultAgoraOps) RootAPIEndpoint(handle any) (string, string) {
	a := handle.(*agent.Agent)
	if a.EnvRoot() == nil {
		return "", "unset"
	}
	return a.EnvRoot().APIEndpoint()
}

func (defaultAgoraOps) EnvironmentAPIEndpoint(handle any) (string, bool) {
	a := handle.(*agent.Agent)
	if a.Environment() == nil {
		return "", false
	}
	return a.Environment().APIEndpoint, true
}

func (defaultAgoraOps) StartRuntime(ctx context.Context, handle any) error {
	return handle.(*agent.Agent).StartRuntime(ctx)
}

func (defaultAgoraOps) EnsureConnected(ctx context.Context, handle any, spec tunnel.ConnectSpec) (*tunnel.ConnectStatus, error) {
	return tunnel.EnsureConnected(ctx, handle.(*agent.Agent), spec)
}

func (defaultAgoraOps) RemoveConnect(ctx context.Context, handle any, name, listenAddress string) error {
	return tunnel.RemoveConnect(ctx, handle.(*agent.Agent), name, listenAddress)
}

func (defaultAgoraOps) EnsureServed(ctx context.Context, handle any, spec tunnel.ServeSpec) (*tunnel.ServeStatus, error) {
	return tunnel.EnsureServed(ctx, handle.(*agent.Agent), spec)
}

func (defaultAgoraOps) RemoveServe(ctx context.Context, handle any, name string) error {
	return tunnel.RemoveServe(ctx, handle.(*agent.Agent), name)
}

func (defaultAgoraOps) EnsurePublished(ctx context.Context, handle any, spec catalog.PublishSpec) (*catalog.Advertisement, error) {
	return catalog.EnsurePublished(ctx, handle.(*agent.Agent), spec)
}

func (defaultAgoraOps) Retract(ctx context.Context, handle any, advertisementID string) error {
	return catalog.Retract(ctx, handle.(*agent.Agent), advertisementID)
}

func (defaultAgoraOps) Close(ctx context.Context, handle any) error {
	return handle.(*agent.Agent).Close(ctx)
}

type agoraSubsystem struct {
	cfg          *Config
	identity     AgoraIdentity
	capabilities []string
	targets      []agoraConnectTarget
	wantRuntime  bool
	ops          agoraOps
	agent        any

	runtimeStarted bool
	advertisement  *catalog.Advertisement
	serveStatus    *tunnel.ServeStatus
	connects       map[string]*tunnel.ConnectStatus
	closed         bool

	log *dl.Builder
}

func newAgoraSubsystem(cfg *Config) (*agoraSubsystem, error) {
	return newAgoraSubsystemWithOps(cfg, defaultAgoraOps{})
}

func newAgoraSubsystemWithOps(cfg *Config, ops agoraOps) (*agoraSubsystem, error) {
	if cfg == nil || cfg.Agora == nil || !cfg.Agora.Enabled {
		return nil, nil
	}
	if ops == nil {
		ops = defaultAgoraOps{}
	}

	identity, err := resolveAgoraIdentity(cfg.Agora)
	if err != nil {
		return nil, err
	}

	targets, err := collectAgoraConnectTargets(cfg)
	if err != nil {
		return nil, err
	}
	wantRuntime := agoraServeEnabled(cfg.Agora) || len(targets) > 0

	if err := validateAgoraConfig(cfg, identity, targets); err != nil {
		return nil, err
	}

	var capabilities []string
	if cfg.Agora.Advertisement != nil {
		capabilities = cfg.Agora.Advertisement.Capabilities
	}
	if len(capabilities) == 0 {
		capabilities = deriveAgoraCapabilities(cfg)
	}

	handle, err := ops.NewStandalone(agent.StandaloneOptions{
		Name:        identity.AgentName,
		Description: identity.Description,
		EnvRoot:     cfg.Agora.EnvRoot,
		WithRuntime: wantRuntime,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize agora agent: %w", err)
	}

	if err := validateAgoraAgentEndpoint(cfg.Agora, wantRuntime, ops, handle); err != nil {
		_ = ops.Close(context.Background(), handle)
		return nil, err
	}

	return &agoraSubsystem{
		cfg:          cfg,
		identity:     identity,
		capabilities: append([]string(nil), capabilities...),
		targets:      targets,
		wantRuntime:  wantRuntime,
		ops:          ops,
		agent:        handle,
		connects:     map[string]*tunnel.ConnectStatus{},
		log:          dl.Log().With("agent", identity.AgentName).With("instance", identity.InstanceName),
	}, nil
}

func validateAgoraConfig(cfg *Config, identity AgoraIdentity, targets []agoraConnectTarget) error {
	agoraCfg := cfg.Agora
	if strings.TrimSpace(agoraCfg.APIEndpoint) == "" {
		return fmt.Errorf("agora.api_endpoint is required when agora is enabled")
	}

	if agoraAdvertisementPublish(agoraCfg) {
		if agoraCfg.Advertisement == nil || len(agoraCfg.Advertisement.WorkgroupIDs) == 0 {
			return fmt.Errorf("agora.advertisement.workgroup_ids requires at least one ID when publishing is enabled")
		}
		for _, id := range agoraCfg.Advertisement.WorkgroupIDs {
			if !agoraWorkgroupIDPattern.MatchString(strings.TrimSpace(id)) {
				return fmt.Errorf("invalid agora workgroup id '%s'", id)
			}
		}
		if agoraCfg.Advertisement.ContractID != "" && !agoraContractIDPattern.MatchString(strings.TrimSpace(agoraCfg.Advertisement.ContractID)) {
			return fmt.Errorf("invalid agora contract id '%s'", agoraCfg.Advertisement.ContractID)
		}
		for _, capability := range agoraCfg.Advertisement.Capabilities {
			if strings.TrimSpace(capability) == "" {
				return fmt.Errorf("agora.advertisement.capabilities cannot contain empty entries")
			}
		}
	}

	if agoraCfg.Serve != nil && agoraCfg.Serve.Enabled && identity.TunnelMode == "http" {
		target := strings.TrimSpace(agoraCfg.Serve.BackendTarget)
		if target == "" {
			target = listenAddress(cfg)
		}
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return fmt.Errorf("agora http tunnel mode requires serve.backend_target with http or https scheme")
		}
	}

	seen := map[string]struct{}{}
	for _, target := range targets {
		if _, ok := seen[target.Key]; ok {
			return fmt.Errorf("duplicate agora provider key '%s'", target.Key)
		}
		seen[target.Key] = struct{}{}
	}

	return nil
}

func validateAgoraAgentEndpoint(cfg *AgoraConfig, wantRuntime bool, ops agoraOps, handle any) error {
	rootEndpoint, source := ops.RootAPIEndpoint(handle)
	if strings.TrimSpace(rootEndpoint) == "" {
		return fmt.Errorf("agora environment api endpoint is not configured")
	}
	if !sameEndpoint(rootEndpoint, cfg.APIEndpoint) {
		return fmt.Errorf("agora.api_endpoint '%s' does not match enrolled environment endpoint '%s' from %s", cfg.APIEndpoint, rootEndpoint, source)
	}

	if wantRuntime {
		envEndpoint, ok := ops.EnvironmentAPIEndpoint(handle)
		if !ok || strings.TrimSpace(envEndpoint) == "" {
			return fmt.Errorf("agora runtime requires an enrolled environment api endpoint")
		}
		if !sameEndpoint(envEndpoint, cfg.APIEndpoint) {
			return fmt.Errorf("agora.api_endpoint '%s' does not match enrolled runtime environment endpoint '%s'", cfg.APIEndpoint, envEndpoint)
		}
	}

	return nil
}

func sameEndpoint(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

func (s *agoraSubsystem) BootstrapConnects(ctx context.Context) error {
	if s == nil || len(s.targets) == 0 {
		return nil
	}
	if err := s.startRuntime(ctx); err != nil {
		return err
	}

	for _, target := range s.targets {
		listenAddress, err := allocateLoopbackPort()
		if err != nil {
			_ = s.Close()
			return err
		}
		status, err := s.ops.EnsureConnected(ctx, s.agent, tunnel.ConnectSpec{
			Name:          target.Service,
			ListenAddress: listenAddress,
		})
		if err != nil {
			_ = s.Close()
			return fmt.Errorf("ensure agora connect for '%s': %w", target.Key, err)
		}
		if status.ListenAddress == "" {
			status.ListenAddress = listenAddress
		}
		s.connects[target.Key] = status
		s.log.Infof("agora connect ready for '%s' service='%s' listen='%s'", target.Key, status.Name, status.ListenAddress)
	}

	return nil
}

func (s *agoraSubsystem) StartServing(ctx context.Context, backendTarget string) error {
	if s == nil {
		return nil
	}

	if agoraServeEnabled(s.cfg.Agora) {
		if err := s.startRuntime(ctx); err != nil {
			return err
		}
		status, err := s.ops.EnsureServed(ctx, s.agent, tunnel.ServeSpec{
			Name:          s.identity.InstanceName,
			Mode:          tunnel.Mode(s.identity.TunnelMode),
			BackendTarget: s.serveBackendTarget(backendTarget),
			GrantEmails:   append([]string(nil), s.cfg.Agora.Serve.Grants...),
		})
		if err != nil {
			_ = s.Close()
			return fmt.Errorf("ensure agora serve: %w", err)
		}
		s.serveStatus = status
		s.log.Infof("agora serve ready name='%s' mode='%s' backend='%s'", status.Name, status.Mode, status.BackendTarget)
	}

	if agoraAdvertisementPublish(s.cfg.Agora) {
		advertisement, err := s.ops.EnsurePublished(ctx, s.agent, catalog.PublishSpec{
			Name:              s.identity.InstanceName,
			Description:       s.identity.Description,
			Capabilities:      s.catalogCapabilities(),
			WorkgroupScopeIDs: append([]string(nil), s.cfg.Agora.Advertisement.WorkgroupIDs...),
			TunnelMode:        catalog.TunnelMode(s.identity.TunnelMode),
			ContractID:        s.cfg.Agora.Advertisement.ContractID,
		})
		if err != nil {
			_ = s.Close()
			return fmt.Errorf("publish agora advertisement: %w", err)
		}
		s.advertisement = advertisement
		s.log.Infof("agora advertisement published id='%s' name='%s'", advertisement.ID, advertisement.Name)
	}

	return nil
}

func (s *agoraSubsystem) startRuntime(ctx context.Context) error {
	if !s.wantRuntime || s.runtimeStarted {
		return nil
	}
	if err := s.ops.StartRuntime(ctx, s.agent); err != nil {
		return fmt.Errorf("start agora runtime: %w", err)
	}
	s.runtimeStarted = true
	s.log.Info("agora runtime started")
	return nil
}

func (s *agoraSubsystem) serveBackendTarget(listenAddress string) string {
	if s.cfg.Agora.Serve != nil && s.cfg.Agora.Serve.BackendTarget != "" {
		return s.cfg.Agora.Serve.BackendTarget
	}
	return listenAddress
}

func (s *agoraSubsystem) catalogCapabilities() []catalog.Capability {
	capabilities := make([]catalog.Capability, 0, len(s.capabilities))
	for _, capability := range s.capabilities {
		capabilities = append(capabilities, catalog.Capability{Name: capability})
	}
	return capabilities
}

func (s *agoraSubsystem) ConnectAddress(providerKey string) (string, bool) {
	if s == nil {
		return "", false
	}
	status, ok := s.connects[providerKey]
	if !ok || status == nil || status.ListenAddress == "" {
		return "", false
	}
	return status.ListenAddress, true
}

func (s *agoraSubsystem) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	if s.advertisement != nil {
		if err := s.withCleanupContext(func(ctx context.Context) error {
			return s.ops.Retract(ctx, s.agent, s.advertisement.ID)
		}); err != nil {
			s.log.Warnf("failed to retract agora advertisement '%s': %v", s.advertisement.ID, err)
			firstErr = err
		}
		s.advertisement = nil
	}

	if s.serveStatus != nil {
		if err := s.withCleanupContext(func(ctx context.Context) error {
			return s.ops.RemoveServe(ctx, s.agent, s.identity.InstanceName)
		}); err != nil {
			s.log.Warnf("failed to remove agora serve '%s': %v", s.identity.InstanceName, err)
			if firstErr == nil {
				firstErr = err
			}
		}
		s.serveStatus = nil
	}

	for key, status := range s.connects {
		status := status
		if status == nil {
			continue
		}
		if err := s.withCleanupContext(func(ctx context.Context) error {
			return s.ops.RemoveConnect(ctx, s.agent, status.Name, status.ListenAddress)
		}); err != nil {
			s.log.Warnf("failed to remove agora connect '%s': %v", key, err)
			if firstErr == nil {
				firstErr = err
			}
		}
		delete(s.connects, key)
	}

	if s.agent != nil {
		if err := s.withCleanupContext(func(ctx context.Context) error {
			return s.ops.Close(ctx, s.agent)
		}); err != nil {
			s.log.Warnf("failed to close agora agent: %v", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func (s *agoraSubsystem) withCleanupContext(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), agoraCleanupTimeout)
	defer cancel()
	return fn(ctx)
}

func collectAgoraConnectTargets(cfg *Config) ([]agoraConnectTarget, error) {
	if cfg == nil || cfg.Providers == nil {
		return nil, nil
	}

	var targets []agoraConnectTarget
	if cfg.Providers.OpenAI != nil {
		target, err := providerAgoraTarget("openai", cfg.Providers.OpenAI.AgoraService, cfg.Providers.OpenAI.ZrokShareToken)
		if err != nil {
			return nil, err
		}
		targets = appendTarget(targets, target)
	}
	if cfg.Providers.Anthropic != nil {
		target, err := providerAgoraTarget("anthropic", cfg.Providers.Anthropic.AgoraService, cfg.Providers.Anthropic.ZrokShareToken)
		if err != nil {
			return nil, err
		}
		targets = appendTarget(targets, target)
	}
	if cfg.Providers.Local != nil {
		if len(cfg.Providers.Local.Endpoints) > 0 {
			for _, ep := range cfg.Providers.Local.Endpoints {
				key := "local:" + strings.TrimSpace(ep.Name)
				target, err := providerAgoraTarget(key, ep.AgoraService, ep.ZrokShareToken)
				if err != nil {
					return nil, err
				}
				if target != nil && strings.TrimSpace(ep.Name) == "" {
					return nil, fmt.Errorf("local endpoint name is required when agora_service is set")
				}
				targets = appendTarget(targets, target)
			}
		} else {
			target, err := providerAgoraTarget("local", cfg.Providers.Local.AgoraService, cfg.Providers.Local.ZrokShareToken)
			if err != nil {
				return nil, err
			}
			targets = appendTarget(targets, target)
		}
	}

	return targets, nil
}

func providerAgoraTarget(key, agoraService, zrokShareToken string) (*agoraConnectTarget, error) {
	agoraService = strings.TrimSpace(os.ExpandEnv(agoraService))
	if agoraService == "" {
		return nil, nil
	}
	if strings.TrimSpace(zrokShareToken) != "" {
		return nil, fmt.Errorf("provider '%s' cannot set both agora_service and zrok_share_token", key)
	}
	return &agoraConnectTarget{Key: key, Service: agoraService}, nil
}

func appendTarget(targets []agoraConnectTarget, target *agoraConnectTarget) []agoraConnectTarget {
	if target == nil {
		return targets
	}
	return append(targets, *target)
}

func agoraServeEnabled(cfg *AgoraConfig) bool {
	return cfg != nil && cfg.Serve != nil && cfg.Serve.Enabled
}

func listenAddress(cfg *Config) string {
	if cfg != nil && cfg.Listen != "" {
		return cfg.Listen
	}
	return ":8080"
}

func allocateLoopbackPort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate agora connect port: %w", err)
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

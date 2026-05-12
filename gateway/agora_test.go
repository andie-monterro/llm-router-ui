package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openziti/agora/sdk/agent"
	"github.com/openziti/agora/sdk/agent/catalog"
	"github.com/openziti/agora/sdk/agent/tunnel"
)

type fakeAgoraOps struct {
	rootEndpoint string
	rootSource   string
	envEndpoint  string

	newOpts      agent.StandaloneOptions
	starts       int
	connectSpecs []tunnel.ConnectSpec
	serveSpecs   []tunnel.ServeSpec
	publishSpecs []catalog.PublishSpec
	removed      []string
	sequence     []string
	closed       int

	connectErr error
	serveErr   error
	publishErr error
}

func (f *fakeAgoraOps) NewStandalone(opts agent.StandaloneOptions) (any, error) {
	f.newOpts = opts
	return "agent", nil
}

func (f *fakeAgoraOps) RootAPIEndpoint(any) (string, string) {
	if f.rootSource == "" {
		f.rootSource = "test"
	}
	return f.rootEndpoint, f.rootSource
}

func (f *fakeAgoraOps) EnvironmentAPIEndpoint(any) (string, bool) {
	return f.envEndpoint, f.envEndpoint != ""
}

func (f *fakeAgoraOps) StartRuntime(context.Context, any) error {
	f.starts++
	f.sequence = append(f.sequence, "start")
	return nil
}

func (f *fakeAgoraOps) EnsureConnected(_ context.Context, _ any, spec tunnel.ConnectSpec) (*tunnel.ConnectStatus, error) {
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	f.connectSpecs = append(f.connectSpecs, spec)
	f.sequence = append(f.sequence, "connect:"+spec.Name)
	return &tunnel.ConnectStatus{Name: spec.Name, ListenAddress: spec.ListenAddress}, nil
}

func (f *fakeAgoraOps) RemoveConnect(_ context.Context, _ any, name, listenAddress string) error {
	f.removed = append(f.removed, "connect:"+name+"@"+listenAddress)
	f.sequence = append(f.sequence, "remove-connect")
	return nil
}

func (f *fakeAgoraOps) EnsureServed(_ context.Context, _ any, spec tunnel.ServeSpec) (*tunnel.ServeStatus, error) {
	if f.serveErr != nil {
		return nil, f.serveErr
	}
	f.serveSpecs = append(f.serveSpecs, spec)
	f.sequence = append(f.sequence, "serve")
	return &tunnel.ServeStatus{Name: spec.Name, Mode: spec.Mode, BackendTarget: spec.BackendTarget}, nil
}

func (f *fakeAgoraOps) RemoveServe(context.Context, any, string) error {
	f.sequence = append(f.sequence, "remove-serve")
	return nil
}

func (f *fakeAgoraOps) EnsurePublished(_ context.Context, _ any, spec catalog.PublishSpec) (*catalog.Advertisement, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.publishSpecs = append(f.publishSpecs, spec)
	f.sequence = append(f.sequence, "publish")
	return &catalog.Advertisement{ID: "adv_abcdefghijkl", Name: spec.Name}, nil
}

func (f *fakeAgoraOps) Retract(context.Context, any, string) error {
	f.sequence = append(f.sequence, "retract")
	return nil
}

func (f *fakeAgoraOps) Close(context.Context, any) error {
	f.closed++
	f.sequence = append(f.sequence, "close")
	return nil
}

func TestAgoraSubsystemBootstrapConnects(t *testing.T) {
	ops := &fakeAgoraOps{rootEndpoint: "http://controller.example", envEndpoint: "http://controller.example"}
	cfg := baseAgoraTestConfig()
	cfg.Agora.Advertisement.Publish = boolPtr(false)
	cfg.Providers = &ProvidersConfig{
		OpenAI: &OpenAIConfig{APIKey: "sk-test", AgoraTunnel: "openai-relay"},
	}

	subsystem, err := newAgoraSubsystemWithOps(cfg, ops)
	if err != nil {
		t.Fatalf("newAgoraSubsystemWithOps returned error: %v", err)
	}
	if !ops.newOpts.WithRuntime {
		t.Fatal("expected standalone agent with runtime")
	}
	if err := subsystem.BootstrapConnects(context.Background()); err != nil {
		t.Fatalf("BootstrapConnects returned error: %v", err)
	}
	if ops.starts != 1 {
		t.Fatalf("StartRuntime calls = %d, want 1", ops.starts)
	}
	if len(ops.connectSpecs) != 1 || ops.connectSpecs[0].Name != "openai-relay" {
		t.Fatalf("unexpected connect specs: %#v", ops.connectSpecs)
	}
	if !strings.HasPrefix(ops.connectSpecs[0].ListenAddress, "127.0.0.1:") {
		t.Fatalf("listen address = %q", ops.connectSpecs[0].ListenAddress)
	}
	if address, ok := subsystem.ConnectAddress("openai"); !ok || address != ops.connectSpecs[0].ListenAddress {
		t.Fatalf("ConnectAddress = %q, %v", address, ok)
	}
}

func TestAgoraSubsystemStartServingPublishesAfterServe(t *testing.T) {
	ops := &fakeAgoraOps{rootEndpoint: "http://controller.example", envEndpoint: "http://controller.example"}
	cfg := baseAgoraTestConfig()
	cfg.Agora.Serve = &AgoraServeConfig{Enabled: true, BackendTarget: "127.0.0.1:8080", Grants: []string{"alice@example.com"}}

	subsystem, err := newAgoraSubsystemWithOps(cfg, ops)
	if err != nil {
		t.Fatalf("newAgoraSubsystemWithOps returned error: %v", err)
	}
	if err := subsystem.StartServing(context.Background(), "127.0.0.1:8080"); err != nil {
		t.Fatalf("StartServing returned error: %v", err)
	}
	if len(ops.serveSpecs) != 1 {
		t.Fatalf("serve specs = %#v", ops.serveSpecs)
	}
	if ops.serveSpecs[0].Name != "engineering" || ops.serveSpecs[0].Mode != tunnel.ModeTCP {
		t.Fatalf("unexpected serve spec: %#v", ops.serveSpecs[0])
	}
	if len(ops.publishSpecs) != 1 {
		t.Fatalf("publish specs = %#v", ops.publishSpecs)
	}
	if ops.publishSpecs[0].Name != "engineering" || ops.publishSpecs[0].TunnelMode != catalog.TunnelTCP {
		t.Fatalf("unexpected publish spec: %#v", ops.publishSpecs[0])
	}
	if got := ops.sequence; len(got) < 3 || got[0] != "start" || got[1] != "serve" || got[2] != "publish" {
		t.Fatalf("unexpected sequence: %#v", got)
	}
}

func TestAgoraSubsystemCloseOrder(t *testing.T) {
	ops := &fakeAgoraOps{rootEndpoint: "http://controller.example", envEndpoint: "http://controller.example"}
	cfg := baseAgoraTestConfig()
	cfg.Agora.Serve = &AgoraServeConfig{Enabled: true, BackendTarget: "127.0.0.1:8080"}
	cfg.Providers = &ProvidersConfig{
		OpenAI: &OpenAIConfig{APIKey: "sk-test", AgoraTunnel: "openai-relay"},
	}

	subsystem, err := newAgoraSubsystemWithOps(cfg, ops)
	if err != nil {
		t.Fatalf("newAgoraSubsystemWithOps returned error: %v", err)
	}
	if err := subsystem.BootstrapConnects(context.Background()); err != nil {
		t.Fatalf("BootstrapConnects returned error: %v", err)
	}
	if err := subsystem.StartServing(context.Background(), "127.0.0.1:8080"); err != nil {
		t.Fatalf("StartServing returned error: %v", err)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	wantSuffix := []string{"retract", "remove-serve", "remove-connect", "close"}
	got := ops.sequence[len(ops.sequence)-len(wantSuffix):]
	for i := range wantSuffix {
		if got[i] != wantSuffix[i] {
			t.Fatalf("cleanup sequence = %#v, want suffix %#v", ops.sequence, wantSuffix)
		}
	}
}

func TestAgoraEndpointMismatchFails(t *testing.T) {
	ops := &fakeAgoraOps{rootEndpoint: "http://other.example", envEndpoint: "http://other.example"}
	if _, err := newAgoraSubsystemWithOps(baseAgoraTestConfig(), ops); err == nil {
		t.Fatal("expected endpoint mismatch error")
	}
}

func TestAgoraConnectFailureCleansUp(t *testing.T) {
	ops := &fakeAgoraOps{
		rootEndpoint: "http://controller.example",
		envEndpoint:  "http://controller.example",
		connectErr:   errors.New("boom"),
	}
	cfg := baseAgoraTestConfig()
	cfg.Agora.Advertisement.Publish = boolPtr(false)
	cfg.Providers = &ProvidersConfig{
		OpenAI: &OpenAIConfig{APIKey: "sk-test", AgoraTunnel: "openai-relay"},
	}

	subsystem, err := newAgoraSubsystemWithOps(cfg, ops)
	if err != nil {
		t.Fatalf("newAgoraSubsystemWithOps returned error: %v", err)
	}
	if err := subsystem.BootstrapConnects(context.Background()); err == nil {
		t.Fatal("expected connect failure")
	}
	if ops.closed != 1 {
		t.Fatalf("Close calls = %d, want 1", ops.closed)
	}
}

func TestAllocateLoopbackPort(t *testing.T) {
	address, err := allocateLoopbackPort()
	if err != nil {
		t.Fatalf("allocateLoopbackPort returned error: %v", err)
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("address = %q", address)
	}
}

func baseAgoraTestConfig() *Config {
	return &Config{
		Agora: &AgoraConfig{
			Enabled:      true,
			APIEndpoint:  "http://controller.example",
			InstanceName: "engineering",
			TunnelMode:   "tcp",
			Advertisement: &AgoraAdvertisementConfig{
				WorkgroupIDs: []string{"wg_abcdefghijkl"},
				ContractID:   "con_abcdefghijkl",
			},
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}

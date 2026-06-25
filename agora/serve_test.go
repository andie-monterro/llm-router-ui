package agora

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openziti/agora/sdk/agent/tunnel"
)

func serveEnabled(cfg *Config) {
	cfg.Serve = &ServeConfig{Enabled: true}
}

func TestServeBindsExistingTunnel(t *testing.T) {
	sub, ops := newTestSubsystem(t, serveEnabled)
	ops.tunnels["engineering"] = "tcp" // operator-provisioned, pre-existing

	sv, err := sub.Serve(context.Background())
	if err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
	if sv.Listener() == nil {
		t.Fatal("expected a bound listener")
	}

	if err := sv.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	// bind-only: Close closes the listener and never deletes the tunnel.
	if len(ops.listeners) != 1 || !ops.listeners[0].closed {
		t.Fatal("bound listener must be closed on Close")
	}
	if _, ok := ops.tunnels["engineering"]; !ok {
		t.Fatal("operator-owned tunnel must be left intact (never deleted)")
	}
}

func TestServeNotProvisionedIsError(t *testing.T) {
	sub, _ := newTestSubsystem(t, serveEnabled)
	// no tunnel provisioned under the serve name

	sv, err := sub.Serve(context.Background())
	if err == nil {
		t.Fatal("expected error binding an unprovisioned tunnel")
	}
	if !errors.Is(err, tunnel.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("expected the directed 'not provisioned' message, got %v", err)
	}
	if sv != nil {
		t.Fatal("expected nil serve when the tunnel is not provisioned")
	}
}

func TestServeWrongModeBindIsError(t *testing.T) {
	sub, ops := newTestSubsystem(t, serveEnabled)
	ops.tunnels["engineering"] = "http" // wrong mode

	sv, err := sub.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "expected tcp") {
		t.Fatalf("expected wrong-mode error, got %v", err)
	}
	if sv != nil {
		t.Fatal("expected nil serve on wrong-mode bind")
	}
	// bind-only validates mode before listening, so no listener is opened.
	if len(ops.listeners) != 0 {
		t.Fatalf("no listener should be opened on a wrong-mode bind: %#v", ops.listeners)
	}
	if _, ok := ops.tunnels["engineering"]; !ok {
		t.Fatal("bound tunnel must be left intact")
	}
}

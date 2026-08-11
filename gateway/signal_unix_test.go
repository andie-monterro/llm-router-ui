//go:build !windows

package gateway

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/openziti/llm-gateway/keys"
)

func TestReloadSignals(t *testing.T) {
	signals := reloadSignals()
	if len(signals) != 1 || signals[0] != syscall.SIGHUP {
		t.Fatalf("reloadSignals() = %#v, want SIGHUP", signals)
	}
	if !isReloadSignal(syscall.SIGHUP) {
		t.Fatal("SIGHUP was not recognized as a reload signal")
	}
	if isReloadSignal(syscall.SIGTERM) {
		t.Fatal("SIGTERM was recognized as a reload signal")
	}
}

func TestDispatchReloadSignalTriggersKeySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.yaml")
	write := func(name, key string) {
		t.Helper()
		content := "version: 1\nkeys:\n  - name: " + name + "\n    key: " + key + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("alice", "sk-alice")
	store, err := keys.NewStoreFromConfig(&keys.Config{
		Enabled: true,
		Sources: []dd.Dynamic{&keys.FileSourceConfig{
			Name: "managed", Path: path, PollInterval: time.Hour,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	gateway := &Gateway{keyStore: store}

	write("bob", "sk-bob")
	if shutdown := gateway.dispatchSignal(syscall.SIGHUP); shutdown {
		t.Fatal("SIGHUP requested shutdown")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if record, ok := store.Lookup("sk-bob"); ok && record.Name == "bob" {
			if !gateway.dispatchSignal(syscall.SIGTERM) {
				t.Fatal("SIGTERM did not request shutdown")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("SIGHUP did not refresh the file source")
}

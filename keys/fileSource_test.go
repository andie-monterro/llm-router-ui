package keys

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func keyFile(name, key string) string {
	return "version: 1\nkeys:\n  - name: " + name + "\n    key: " + key + "\n"
}

func writeKeyFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileSourceLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.yaml")
	writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	source, err := newFileSource(&FileSourceConfig{Name: "managed", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	result, err := source.Load(t.Context())
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if err := result.validate(); err != nil {
		t.Fatal(err)
	}
	if result.IsUnchanged() || result.Contribution().Records[0].Name != "alice" {
		t.Fatalf("result = %#v", result)
	}
}

func TestFileSourceWatchDebouncesEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.yaml")
	writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	source, err := newFileSource(&FileSourceConfig{Name: "managed", Path: path, Watch: true})
	if err != nil {
		t.Fatal(err)
	}
	// the watcher is live from newFileSource, so writing before Watch starts
	// leaves the burst buffered in fsnotify's channel. Watch then drains it in
	// one tight loop, which is what makes the collapse deterministic: issuing
	// the writes while Watch is already running races them against the debounce
	// timer, and a loaded runner straddles the window.
	source.debounce = 250 * time.Millisecond
	t.Cleanup(func() { source.close() })

	for i := 0; i < 4; i++ {
		writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	notified := make(chan struct{}, 8)
	done := make(chan error, 1)
	go func() {
		done <- source.Watch(ctx, func() { notified <- struct{}{} })
	}()

	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not notify")
	}
	// assert quiescence rather than counting after a fixed sleep: the property
	// is that no second notification follows the burst.
	select {
	case <-notified:
		t.Fatal("burst produced more than one debounced notification")
	case <-time.After(4 * source.debounce):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch() = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch() did not stop after cancellation")
	}
}

func TestFileSourceWatchRecognizesConfigMapSwap(t *testing.T) {
	source := &fileSource{path: "/etc/keys/keys.yaml"}
	if !source.relevant("/etc/keys/..data") {
		t.Fatal("ConfigMap ..data swap was not recognized")
	}
	if source.relevant("/etc/keys/unrelated") {
		t.Fatal("unrelated directory event was recognized")
	}
}

func TestFileSourceWatchSetupFailureIsBootError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "keys.yaml")
	_, err := newFileSource(&FileSourceConfig{Name: "managed", Path: path, Watch: true})
	if err == nil || !strings.Contains(err.Error(), "watch directory") {
		t.Fatalf("newFileSource() = %v, want watcher setup error", err)
	}
}

func TestFileSourceAtomicRenameRefreshesStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.yaml")
	writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	cfg := &Config{Enabled: true, Sources: dynamics(&FileSourceConfig{
		Name: "managed", Path: path, Watch: true, PollInterval: time.Hour,
	})}
	store, err := NewStoreFromConfig(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	temporary := filepath.Join(dir, "keys.next")
	writeKeyFile(t, temporary, keyFile("bob", "sk-bob"))
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if record, ok := store.Lookup("sk-bob"); ok && record.Name == "bob" {
			if _, oldOK := store.Lookup("sk-alice"); oldOK {
				t.Fatal("old key survived atomic replacement")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("atomic rename did not refresh the resident snapshot")
}

func TestFileSourceConfigMapSymlinkSwapRefreshesStore(t *testing.T) {
	dir := t.TempDir()
	versionOne := filepath.Join(dir, "..2026_08_11_1")
	if err := os.Mkdir(versionOne, 0o700); err != nil {
		t.Fatal(err)
	}
	writeKeyFile(t, filepath.Join(versionOne, "keys.yaml"), keyFile("alice", "sk-alice"))
	dataLink := filepath.Join(dir, "..data")
	if err := os.Symlink(filepath.Base(versionOne), dataLink); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keys.yaml")
	if err := os.Symlink(filepath.Join("..data", "keys.yaml"), path); err != nil {
		t.Fatal(err)
	}

	store, err := NewStoreFromConfig(&Config{Enabled: true, Sources: dynamics(&FileSourceConfig{
		Name: "managed", Path: path, Watch: true, PollInterval: time.Hour,
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	versionTwo := filepath.Join(dir, "..2026_08_11_2")
	if err := os.Mkdir(versionTwo, 0o700); err != nil {
		t.Fatal(err)
	}
	writeKeyFile(t, filepath.Join(versionTwo, "keys.yaml"), keyFile("bob", "sk-bob"))
	temporaryLink := filepath.Join(dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(versionTwo), temporaryLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryLink, dataLink); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if record, ok := store.Lookup("sk-bob"); ok && record.Name == "bob" {
			if _, oldOK := store.Lookup("sk-alice"); oldOK {
				t.Fatal("old key survived ConfigMap symlink replacement")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ConfigMap symlink swap did not refresh the resident snapshot")
}

func TestFileRefreshFailureHoldsLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.yaml")
	writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	cfg := &Config{Enabled: true, Sources: dynamics(&FileSourceConfig{
		Name: "managed", Path: path, PollInterval: time.Hour,
	})}
	store, err := NewStoreFromConfig(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store.runners[0].refresh(t.Context())
	if record, ok := store.Lookup("sk-alice"); !ok || record.Name != "alice" {
		t.Fatalf("last-known-good lookup = %+v, %v", record, ok)
	}
}

func TestFileSourceBootRequiredPolicy(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	required := &Config{Enabled: true, Sources: dynamics(&FileSourceConfig{
		Name: "required", Path: missing, PollInterval: time.Hour,
	})}
	if _, err := NewStoreFromConfig(required, nil); err == nil || !strings.Contains(err.Error(), "at boot") {
		t.Fatalf("NewStoreFromConfig(required) = %v, want boot error", err)
	}

	falseValue := false
	optional := &Config{
		Enabled: true,
		Keys:    []EntryConfig{{Name: "breakglass", Key: "sk-breakglass"}},
		Sources: dynamics(&FileSourceConfig{
			Name: "optional", Path: missing, PollInterval: time.Hour, Required: &falseValue,
		}),
	}
	store, err := NewStoreFromConfig(optional, nil)
	if err != nil {
		t.Fatalf("NewStoreFromConfig(optional) = %v, want nil", err)
	}
	t.Cleanup(func() { store.Close() })
	if record, ok := store.Lookup("sk-breakglass"); !ok || record.Name != "breakglass" {
		t.Fatalf("breakglass lookup = %+v, %v", record, ok)
	}
}

func TestBootCompositionDoesNotWarnForTransientEmptyConfigContribution(t *testing.T) {
	logs := captureKeyLogs(t)
	path := filepath.Join(t.TempDir(), "keys.yaml")
	writeKeyFile(t, path, keyFile("alice", "sk-alice"))
	store, err := NewStoreFromConfig(&Config{Enabled: true, Sources: dynamics(&FileSourceConfig{
		Name: "managed", Path: path, PollInterval: time.Hour,
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if strings.Contains(logs.String(), "snapshot is empty") {
		t.Fatalf("healthy boot logged a transient deny-all snapshot: %q", logs.String())
	}
}

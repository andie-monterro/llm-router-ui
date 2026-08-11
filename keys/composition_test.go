package keys

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michaelquigley/df/dl"
)

type staticSource struct {
	name string
}

func (s *staticSource) Name() string {
	return s.name
}

func (s *staticSource) Load(context.Context) (LoadResult, error) {
	return Updated(&Contribution{SchemaVersion: 1}), nil
}

func newCompositionStore(t *testing.T, names ...string) *Store {
	t.Helper()
	store := newEmptyStore(time.Now)
	for _, name := range names {
		if err := store.registerSource(&staticSource{name: name}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	store.finishBoot()
	t.Cleanup(func() { store.Close() })
	return store
}

func contributionFor(t *testing.T, name, key string) *Contribution {
	t.Helper()
	digest, err := HashKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return &Contribution{SchemaVersion: 1, Records: []*Record{{Name: name, Digest: digest}}}
}

func captureKeyLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	opts := dl.DefaultOptions()
	opts.Level = slog.LevelDebug
	opts.UseJSON = false
	opts.UseColor = false
	opts.Output = &output
	dl.Init(opts)
	t.Cleanup(func() { dl.Init() })
	return &output
}

func TestStoreCompositionPrecedenceAndDeletionPromotion(t *testing.T) {
	logs := captureKeyLogs(t)
	store := newCompositionStore(t, "config", "managed")
	when := time.Now()
	if err := store.Install("config", contributionFor(t, "breakglass", "sk-shared"), when); err != nil {
		t.Fatal(err)
	}
	if err := store.Install("managed", contributionFor(t, "remote", "sk-shared"), when); err != nil {
		t.Fatal(err)
	}
	record, ok := store.Lookup("sk-shared")
	if !ok || record.Name != "breakglass" || record.Source != "config" {
		t.Fatalf("precedence lookup = %+v, %v", record, ok)
	}
	if text := logs.String(); !strings.Contains(text, "breakglass") || !strings.Contains(text, "remote") ||
		!strings.Contains(text, "config") || !strings.Contains(text, "managed") || strings.Contains(text, "sk-shared") {
		t.Fatalf("collision log = %q", text)
	}

	if err := store.Install("config", &Contribution{SchemaVersion: 1}, when.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	record, ok = store.Lookup("sk-shared")
	if !ok || record.Name != "remote" || record.Source != "managed" {
		t.Fatalf("deletion promotion lookup = %+v, %v", record, ok)
	}
}

func TestStoreExcludedWinnerDoesNotPromoteDuplicate(t *testing.T) {
	store := newCompositionStore(t, "config", "managed")
	when := time.Now()
	if err := store.Install("config", contributionFor(t, "breakglass", "sk-shared"), when); err != nil {
		t.Fatal(err)
	}
	if err := store.Install("managed", contributionFor(t, "remote", "sk-shared"), when); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.states["config"].excluded = true
	store.recompose()
	store.mu.Unlock()
	if record, ok := store.Lookup("sk-shared"); ok {
		t.Fatalf("excluded winning digest promoted lower record: %+v", record)
	}
}

func TestStoreNilContributionParticipatesInNeitherPass(t *testing.T) {
	store := newCompositionStore(t, "optional", "healthy")
	if err := store.Install("healthy", contributionFor(t, "alice", "sk-alice"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if record, ok := store.Lookup("sk-alice"); !ok || record.Name != "alice" {
		t.Fatalf("lookup = %+v, %v", record, ok)
	}
}

func TestStoreInstallTakesDeepOwnership(t *testing.T) {
	store := newCompositionStore(t, "managed")
	expiresAt := time.Now().Add(time.Hour)
	digest, err := HashKey("sk-alice")
	if err != nil {
		t.Fatal(err)
	}
	record := &Record{
		Name:          "alice",
		Digest:        digest,
		AllowedModels: []string{"gpt-*"},
		AllowedRoutes: []string{"coding"},
		ExpiresAt:     &expiresAt,
	}
	contribution := &Contribution{SchemaVersion: 1, Records: []*Record{record}}
	if err := store.Install("managed", contribution, time.Now()); err != nil {
		t.Fatal(err)
	}

	record.Name = "mutated"
	record.AllowedModels[0] = "other-*"
	record.AllowedRoutes[0] = "other"
	*record.ExpiresAt = time.Now().Add(-time.Hour)
	contribution.Records = nil

	resident, ok := store.Lookup("sk-alice")
	if !ok || resident.Name != "alice" || resident.AllowedModels[0] != "gpt-*" ||
		resident.AllowedRoutes[0] != "coding" || resident.Expired(time.Now()) {
		t.Fatalf("resident record changed through source alias: %+v, %v", resident, ok)
	}
}

func TestStoreConcurrentInstallsComposeBothSources(t *testing.T) {
	store := newCompositionStore(t, "a", "b")
	when := time.Now()
	var wg sync.WaitGroup
	for _, item := range []struct {
		source       string
		contribution *Contribution
	}{{"a", contributionFor(t, "alice", "sk-alice")}, {"b", contributionFor(t, "bob", "sk-bob")}} {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Install(item.source, item.contribution, when); err != nil {
				t.Errorf("Install(%s) = %v", item.source, err)
			}
		}()
	}
	wg.Wait()
	for _, key := range []string{"sk-alice", "sk-bob"} {
		if _, ok := store.Lookup(key); !ok {
			t.Errorf("Lookup(%s) failed after concurrent installs", key)
		}
	}
}

func TestStoreTouchRequiresResidentContribution(t *testing.T) {
	store := newCompositionStore(t, "optional")
	if err := store.Touch("optional", time.Now()); err == nil || !strings.Contains(err.Error(), "no contribution") {
		t.Fatalf("Touch() = %v, want no-contribution error", err)
	}
}

func TestStoreWarnsOnDuplicateNameAndEmptyUnion(t *testing.T) {
	logs := captureKeyLogs(t)
	store := newCompositionStore(t, "a", "b")
	if err := store.Install("a", contributionFor(t, "alice", "sk-a"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Install("b", contributionFor(t, "alice", "sk-b"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "name 'alice' is shared") {
		t.Fatalf("duplicate-name log = %q", logs.String())
	}
	if err := store.Install("a", &Contribution{SchemaVersion: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Install("b", &Contribution{SchemaVersion: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "snapshot is empty") {
		t.Fatalf("empty-union log = %q", logs.String())
	}
}

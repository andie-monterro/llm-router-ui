package keys

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestHashKeyBearerGrammar(t *testing.T) {
	for _, value := range []string{"a", "sk-gw-abc_123", "abc==", "~+/"} {
		t.Run("accept "+value, func(t *testing.T) {
			got, err := HashKey(value)
			if err != nil {
				t.Fatalf("HashKey() = %v, want nil", err)
			}
			if want := sha256.Sum256([]byte(value)); got != want {
				t.Errorf("HashKey() digest differs from exact-byte SHA-256")
			}
		})
	}

	for _, value := range []string{"", "has space", "trailing ", "café", "abc=def", "line\nbreak"} {
		t.Run("reject "+value, func(t *testing.T) {
			_, err := HashKey(value)
			if err == nil {
				t.Fatal("HashKey() = nil, want grammar error")
			}
			if value != "" && strings.Contains(err.Error(), value) {
				t.Error("HashKey() error exposed key material")
			}
		})
	}
}

func TestParseDigestCaseInsensitive(t *testing.T) {
	want := sha256.Sum256([]byte("test"))
	upper := strings.ToUpper("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08")
	got, err := ParseDigest(upper)
	if err != nil {
		t.Fatalf("ParseDigest() = %v, want nil", err)
	}
	if got != want {
		t.Errorf("ParseDigest() = %x, want %x", got, want)
	}

	secret := "not-a-valid-digest-value"
	if _, err := ParseDigest(secret); err == nil {
		t.Fatal("ParseDigest() = nil, want error")
	} else if strings.Contains(err.Error(), secret) {
		t.Error("ParseDigest() error exposed input")
	}
}

func TestRecordExpiryBoundary(t *testing.T) {
	expires := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	record := &Record{ExpiresAt: &expires}
	if record.Expired(expires.Add(-time.Nanosecond)) {
		t.Error("record expired before its boundary")
	}
	if !record.Expired(expires) {
		t.Error("record remained valid at its boundary")
	}
	if !record.Expired(expires.Add(time.Nanosecond)) {
		t.Error("record remained valid after its boundary")
	}
}

func TestRecordRestrictions(t *testing.T) {
	tests := []struct {
		name    string
		record  Record
		value   string
		allowed bool
		model   bool
	}{
		{"model unrestricted", Record{}, "gpt-4", true, true},
		{"model glob", Record{AllowedModels: []string{"claude-*"}}, "claude-sonnet", true, true},
		{"model denied", Record{AllowedModels: []string{"claude-*"}}, "gpt-4", false, true},
		{"wildcard does not cross slash", Record{AllowedModels: []string{"*"}}, "meta-llama/Llama-3", false, true},
		{"route unrestricted", Record{}, "coding", true, false},
		{"route exact", Record{AllowedRoutes: []string{"coding"}}, "coding", true, false},
		{"route literal", Record{AllowedRoutes: []string{"cod*"}}, "coding", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bool
			if tt.model {
				got = tt.record.AllowsModel(tt.value)
			} else {
				got = tt.record.AllowsRoute(tt.value)
			}
			if got != tt.allowed {
				t.Errorf("restriction result = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestConfigSourceRejectsInvalidRestrictions(t *testing.T) {
	tests := []struct {
		name  string
		entry EntryConfig
		path  string
	}{
		{"bad model glob", EntryConfig{Name: "alice", Key: "sk-alice", AllowedModels: []string{"["}}, "allowed_models[0]"},
		{"empty route", EntryConfig{Name: "alice", Key: "sk-alice", AllowedRoutes: []string{""}}, "allowed_routes[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newConfigSource([]EntryConfig{tt.entry}).load(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.path) {
				t.Fatalf("load() = %v, want error containing %q", err, tt.path)
			}
		})
	}
}

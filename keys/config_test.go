package keys

import (
	"strings"
	"testing"
	"time"

	"github.com/michaelquigley/df/dd"
)

func TestBindConfigStrictInlineRecords(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantErr string
		secret  string
	}{
		{
			name: "valid",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": "alice", "key": "sk-alice"},
			}},
		},
		{
			name: "unknown block field",
			raw: map[string]any{"enabled": true, "typo": true, "keys": []any{
				map[string]any{"name": "alice", "key": "sk-alice"},
			}},
			wantErr: "api_keys: unknown field 'typo'",
		},
		{
			name: "unknown record field",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": "alice", "key": "sk-alice", "allowed_model": "gpt-*"},
			}},
			wantErr: "api_keys.keys[0]: unknown field 'allowed_model'",
		},
		{
			name: "coercible name",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": 20260101, "key": "sk-alice"},
			}},
			wantErr: "api_keys.keys[0].name",
		},
		{
			name: "secret value type",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": "alice", "key": 12345678},
			}},
			wantErr: "api_keys.keys[0].key",
			secret:  "12345678",
		},
		{
			name: "allowed models value type",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": "alice", "key": "sk-alice", "allowed_models": "not-a-list"},
			}},
			wantErr: "api_keys.keys[0].allowed_models",
		},
		{
			name: "allowed routes value type",
			raw: map[string]any{"enabled": true, "keys": []any{
				map[string]any{"name": "alice", "key": "sk-alice", "allowed_routes": 5},
			}},
			wantErr: "api_keys.keys[0].allowed_routes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BindConfig(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("BindConfig() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("BindConfig() = %v, want error containing %q", err, tt.wantErr)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Errorf("BindConfig() error exposed key material: %v", err)
			}
		})
	}
}

func TestBindConfigNullableRestrictions(t *testing.T) {
	raw := map[string]any{"enabled": true, "keys": []any{
		map[string]any{
			"name":           "alice",
			"key":            "sk-alice",
			"allowed_models": nil,
			"allowed_routes": nil,
			"expires_at":     nil,
		},
	}}
	cfg, err := BindConfig(raw)
	if err != nil {
		t.Fatalf("BindConfig() = %v, want nil", err)
	}
	if cfg.Keys[0].AllowedModels != nil || cfg.Keys[0].AllowedRoutes != nil {
		t.Fatalf("nullable restrictions = %#v / %#v, want nil", cfg.Keys[0].AllowedModels, cfg.Keys[0].AllowedRoutes)
	}
}

func TestBindConfigRequiredNullStillRejects(t *testing.T) {
	for _, field := range []string{"name", "key"} {
		t.Run(field, func(t *testing.T) {
			record := map[string]any{"name": "alice", "key": "sk-alice"}
			record[field] = nil
			_, err := BindConfig(map[string]any{"enabled": true, "keys": []any{record}})
			if err == nil || !strings.Contains(err.Error(), "api_keys.keys[0]."+field) {
				t.Fatalf("BindConfig() = %v, want null rejection for %s", err, field)
			}
		})
	}
}

func TestResolveConfig(t *testing.T) {
	t.Setenv("LLMGW_KEYS_TEST", "sk-resolved")
	cfg := &Config{Enabled: true, Keys: []EntryConfig{{Name: "alice", Key: "${LLMGW_KEYS_TEST}"}}}
	if err := ResolveConfig(cfg); err != nil {
		t.Fatalf("ResolveConfig() = %v, want nil", err)
	}
	if cfg.Keys[0].Key != "sk-resolved" {
		t.Errorf("resolved key = %q, want sk-resolved", cfg.Keys[0].Key)
	}

	cfg.Keys[0].Key = "${LLMGW_KEYS_UNSET}"
	if err := ResolveConfig(cfg); err == nil || !strings.Contains(err.Error(), "resolves empty") {
		t.Fatalf("ResolveConfig() = %v, want resolves-empty error", err)
	}

	disabled := &Config{Keys: []EntryConfig{{Name: "alice"}}}
	if err := ResolveConfig(disabled); err != nil {
		t.Fatalf("ResolveConfig(disabled) = %v, want nil", err)
	}
}

func TestBindConfigFileSources(t *testing.T) {
	raw := map[string]any{
		"enabled": true,
		"keys":    []any{map[string]any{"name": "breakglass", "key": "sk-breakglass"}},
		"sources": []any{
			map[string]any{"type": "file", "path": "/tmp/keys.yaml"},
			map[string]any{"type": "file", "name": "managed", "path": "/tmp/managed.yaml", "poll_interval": "5s", "required": false},
		},
		"reload": map[string]any{"max_staleness": 0},
	}
	cfg, err := BindConfig(raw)
	if err != nil {
		t.Fatalf("BindConfig() = %v, want nil", err)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("sources = %#v", cfg.Sources)
	}
	first, ok := cfg.Sources[0].(*FileSourceConfig)
	if !ok || first.PollInterval != defaultPollInterval || first.required() != true {
		t.Fatalf("default file source = %#v", cfg.Sources[0])
	}
	second := cfg.Sources[1].(*FileSourceConfig)
	if second.PollInterval != 5*time.Second || second.required() != false {
		t.Fatalf("explicit file source = %#v", second)
	}
	if cfg.Reload == nil || cfg.Reload.MaxStaleness != 0 {
		t.Fatalf("reload = %#v", cfg.Reload)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if first.Name != "file[0]" || second.Name != "managed" {
		t.Errorf("source identities = %q / %q", first.Name, second.Name)
	}
}

func TestBindConfigRejectsInvalidSourceSurface(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantErr string
	}{
		{
			"unknown reload field",
			map[string]any{"enabled": true, "keys": []any{map[string]any{"name": "a", "key": "sk-a"}}, "reload": map[string]any{"max_stalenes": "1h"}},
			"api_keys.reload: unknown field 'max_stalenes'",
		},
		{
			"unknown source field",
			map[string]any{"enabled": true, "sources": []any{map[string]any{"type": "file", "path": "/tmp/keys", "wach": true}}},
			"api_keys.sources[0]: unknown field 'wach'",
		},
		{
			"unknown source type",
			map[string]any{"enabled": true, "sources": []any{map[string]any{"type": "postgres"}}},
			"unknown Dynamic type \"postgres\"",
		},
		{
			"missing source type",
			map[string]any{"enabled": true, "sources": []any{map[string]any{"path": "/tmp/keys"}}},
			"missing 'type' discriminator",
		},
		{
			"bare poll interval",
			map[string]any{"enabled": true, "sources": []any{map[string]any{"type": "file", "path": "/tmp/keys", "poll_interval": 30}}},
			"expected duration string",
		},
		{
			"fractional poll interval",
			map[string]any{"enabled": true, "sources": []any{map[string]any{"type": "file", "path": "/tmp/keys", "poll_interval": 1.5}}},
			"expected duration string",
		},
		{
			"bare nonzero max staleness",
			map[string]any{"enabled": true, "keys": []any{map[string]any{"name": "a", "key": "sk-a"}}, "reload": map[string]any{"max_staleness": 3600}},
			"expected duration string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BindConfig(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("BindConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	falseValue := false
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{"enabled empty", &Config{Enabled: true}, "at least one configured key or source"},
		{"disabled sources", &Config{Sources: dynamics(fileDynamic("a", "/tmp/a", time.Second, nil))}, "requires api_keys.enabled"},
		{"reserved name", &Config{Enabled: true, Sources: dynamics(fileDynamic("config", "/tmp/a", time.Second, nil))}, "reserved or duplicated"},
		{"duplicate name", &Config{Enabled: true, Sources: dynamics(fileDynamic("same", "/tmp/a", time.Second, nil), fileDynamic("same", "/tmp/b", time.Second, nil))}, "reserved or duplicated"},
		{"empty path", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", " ", time.Second, nil))}, ".path must not be empty"},
		{"zero poll", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", 0, nil))}, ".poll_interval must be positive"},
		{"negative poll", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", -time.Second, nil))}, ".poll_interval must be positive"},
		{"negative staleness", &Config{Enabled: true, Keys: []EntryConfig{{Name: "a", Key: "sk-a"}}, Reload: &ReloadConfig{MaxStaleness: -time.Second}}, "max_staleness must not be negative"},
		{"positive staleness without reloadable source", &Config{Enabled: true, Keys: []EntryConfig{{Name: "a", Key: "sk-a"}}, Reload: &ReloadConfig{MaxStaleness: time.Hour}}, ""},
		{"positive staleness accommodates source", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", time.Second, nil)), Reload: &ReloadConfig{MaxStaleness: 2 * time.Second}}, ""},
		{"staleness equal to poll", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", time.Second, nil)), Reload: &ReloadConfig{MaxStaleness: time.Second}}, "must be greater than"},
		{"staleness below poll", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", 2*time.Second, nil)), Reload: &ReloadConfig{MaxStaleness: time.Second}}, "must be greater than"},
		{"disabled negative staleness", &Config{Reload: &ReloadConfig{MaxStaleness: -time.Second}}, "max_staleness must not be negative"},
		{"disabled positive staleness", &Config{Reload: &ReloadConfig{MaxStaleness: time.Hour}}, ""},
		{"optional source valid", &Config{Enabled: true, Sources: dynamics(fileDynamic("a", "/tmp/a", time.Second, &falseValue))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func dynamics(values ...*FileSourceConfig) []dd.Dynamic {
	result := make([]dd.Dynamic, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func fileDynamic(name, path string, interval time.Duration, required *bool) *FileSourceConfig {
	return &FileSourceConfig{Name: name, Path: path, PollInterval: interval, Required: required}
}

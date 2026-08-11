package keys

import (
	"strings"
	"testing"
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

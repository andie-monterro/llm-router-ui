package keys

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDecodeYAMLContribution(t *testing.T) {
	data := []byte(`version: 1
keys:
  - name: alice
    key: sk-alice
    allowed_models: [gpt-*]
    allowed_routes: [coding]
    expires_at: "2026-12-31T23:59:59Z"
  - name: bob
    key_sha256: 6F8F57715090DA2632453988D9A1501B157399D95979A0D0C77FF9884D7693E1
`)
	contribution, err := decodeYAMLContribution(data)
	if err != nil {
		t.Fatalf("decodeYAMLContribution() = %v, want nil", err)
	}
	if contribution.SchemaVersion != 1 || len(contribution.Records) != 2 {
		t.Fatalf("contribution = %#v", contribution)
	}
	if contribution.Records[0].Digest != sha256.Sum256([]byte("sk-alice")) {
		t.Error("plaintext key was not hashed over its exact bytes")
	}
	if contribution.Records[0].ExpiresAt == nil ||
		!contribution.Records[0].ExpiresAt.Equal(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("expiry = %v", contribution.Records[0].ExpiresAt)
	}
}

func TestDecodeYAMLNullableRestrictions(t *testing.T) {
	contribution, err := decodeYAMLContribution([]byte(`version: 1
keys:
  - name: alice
    key: sk-alice
    allowed_models: null
    allowed_routes: null
    expires_at: null
`))
	if err != nil {
		t.Fatalf("decodeYAMLContribution() = %v, want nil", err)
	}
	record := contribution.Records[0]
	if record.AllowedModels != nil || record.AllowedRoutes != nil || record.ExpiresAt != nil {
		t.Fatalf("nullable fields = %#v / %#v / %v", record.AllowedModels, record.AllowedRoutes, record.ExpiresAt)
	}
}

func TestDecodeYAMLRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"unknown envelope field", "version: 1\nkeys: []\ntyop: true\n", "unknown field"},
		{"unknown record field", "version: 1\nkeys:\n  - name: alice\n    key: sk-alice\n    allowed_model: '*'\n", "keys[0].allowed_model"},
		{"invalid restriction item", "version: 1\nkeys:\n  - name: alice\n    key: sk-alice\n    allowed_models: [5]\n", "keys[0].allowed_models[0]"},
		{"duplicate field", "version: 1\nkeys:\n  - name: alice\n    name: bob\n    key: sk-alice\n", "keys[0]"},
		{"multiple documents", "version: 1\nkeys: []\n---\nversion: 1\nkeys: []\n", "multi-document"},
		{"missing version", "keys: []\n", "required field"},
		{"negative version", "version: -1\nkeys: []\n", "must not be negative"},
		{"unknown version", "version: 2\nkeys: []\n", "unsupported key schema version 2"},
		{"missing keys", "version: 1\n", "required field"},
		{"null keys", "version: 1\nkeys: null\n", "keys"},
		{"file count", "version: 1\ncount: 0\nkeys: []\n", "count is not valid"},
		{"null file count", "version: 1\ncount: null\nkeys: []\n", "count is not valid"},
		{"null name", "version: 1\nkeys:\n  - name: null\n    key: sk-alice\n", "keys[0].name"},
		{"null key", "version: 1\nkeys:\n  - name: alice\n    key: null\n", "keys[0].key"},
		{"invalid model pattern", "version: 1\nkeys:\n  - name: alice\n    key: sk-alice\n    allowed_models: ['[']\n", "allowed_models[0]"},
		{"empty route", "version: 1\nkeys:\n  - name: alice\n    key: sk-alice\n    allowed_routes: ['']\n", "allowed_routes[0]"},
		{"malformed digest", "version: 1\nkeys:\n  - name: alice\n    key_sha256: not-a-digest\n", "key_sha256"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeYAMLContribution([]byte(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("decodeYAMLContribution() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeYAMLDuplicateDigestRejectsWithoutSecret(t *testing.T) {
	secret := "sk-shared"
	doc := "version: 1\nkeys:\n  - name: alice\n    key: " + secret + "\n  - name: bob\n    key: " + secret + "\n"
	_, err := decodeYAMLContribution([]byte(doc))
	if err == nil || !strings.Contains(err.Error(), "'alice' and 'bob'") {
		t.Fatalf("decodeYAMLContribution() = %v, want duplicate error naming both entries", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("duplicate error exposed key material: %v", err)
	}
}

func TestDecodeYAMLKeyMaterialPresence(t *testing.T) {
	digest := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		fields  string
		wantErr string
	}{
		{"neither", "", "exactly one"},
		{"key only", "    key: sk-alice\n", ""},
		{"digest only", "    key_sha256: " + digest + "\n", ""},
		{"both", "    key: sk-alice\n    key_sha256: " + digest + "\n", "must not both"},
		{"empty key with digest", "    key: \"\"\n    key_sha256: " + digest + "\n", "must not both"},
		{"key with empty digest", "    key: sk-alice\n    key_sha256: \"\"\n", "must not both"},
		{"empty key", "    key: \"\"\n", ".key must not be empty"},
		{"empty digest", "    key_sha256: \"\"\n", ".key_sha256 must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := "version: 1\nkeys:\n  - name: alice\n" + tt.fields
			_, err := decodeYAMLContribution([]byte(doc))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("decodeYAMLContribution() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("decodeYAMLContribution() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeYAMLErrorsNeverExposeKeyMaterial(t *testing.T) {
	for _, field := range []string{"key", "key_sha256"} {
		for _, secret := range []string{"0xdeadbeef", "0o755", "007", ".nan"} {
			name := field + "_" + secret
			t.Run(name, func(t *testing.T) {
				doc := "version: 1\nkeys:\n  - name: alice\n    " + field + ": " + secret + "\n"
				_, err := decodeYAMLContribution([]byte(doc))
				if err == nil {
					t.Fatal("decodeYAMLContribution() succeeded, want rejection")
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error exposed key material %q: %v", secret, err)
				}
				if !strings.Contains(err.Error(), "keys[0]."+field) {
					t.Fatalf("error = %v, want structural field path", err)
				}
			})
		}
	}
}

func TestDecodeYAMLTimestampNamesQuotingFix(t *testing.T) {
	document := struct {
		Version int `yaml:"version"`
		Keys    []struct {
			Name      string    `yaml:"name"`
			Key       string    `yaml:"key"`
			ExpiresAt time.Time `yaml:"expires_at"`
		} `yaml:"keys"`
	}{Version: 1}
	document.Keys = append(document.Keys, struct {
		Name      string    `yaml:"name"`
		Key       string    `yaml:"key"`
		ExpiresAt time.Time `yaml:"expires_at"`
	}{Name: "alice", Key: "sk-alice", ExpiresAt: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)})
	data, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decodeYAMLContribution(data)
	if err == nil || !strings.Contains(err.Error(), "keys[0].expires_at") || !strings.Contains(err.Error(), "quote") {
		t.Fatalf("decodeYAMLContribution() = %v, want directed quoting error", err)
	}
	if strings.Contains(err.Error(), "scalar tag") {
		t.Fatalf("operator error leaked decoder jargon: %v", err)
	}
}

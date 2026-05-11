package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgoraIntegrationFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agora.yaml")
	if err := os.WriteFile(path, []byte(`
api_endpoint: "http://127.0.0.1:8080"
env_root: "/tmp/agora"
advertisement:
  workgroup_ids:
    - wg_abcdefghijkl
  contract_id: con_abcdefghijkl
`), 0o600); err != nil {
		t.Fatalf("write integration file: %v", err)
	}

	file, err := loadAgoraIntegrationFile(path)
	if err != nil {
		t.Fatalf("loadAgoraIntegrationFile returned error: %v", err)
	}
	if file.APIEndpoint != "http://127.0.0.1:8080" || file.EnvRoot != "/tmp/agora" {
		t.Fatalf("unexpected file: %#v", file)
	}
	if got := file.Advertisement.WorkgroupIDs; len(got) != 1 || got[0] != "wg_abcdefghijkl" {
		t.Fatalf("unexpected workgroup IDs: %#v", got)
	}
}

func TestLoadAgoraIntegrationFileMissing(t *testing.T) {
	if _, err := loadAgoraIntegrationFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestMergeAgoraIntegrationFile(t *testing.T) {
	cfg := &AgoraConfig{
		APIEndpoint: "http://inline.example",
		Advertisement: &AgoraAdvertisementConfig{
			ContractID: "con_inline1234",
		},
	}
	file := &AgoraIntegrationFile{
		APIEndpoint: "http://file.example",
		EnvRoot:     "/tmp/file",
		Advertisement: &AgoraIntegrationAdvertisement{
			WorkgroupIDs: []string{"wg_abcdefghijkl"},
			ContractID:   "con_abcdefghijkl",
		},
	}

	mergeAgoraIntegrationFile(cfg, file)

	if cfg.APIEndpoint != "http://inline.example" {
		t.Fatalf("inline API endpoint was overwritten: %q", cfg.APIEndpoint)
	}
	if cfg.EnvRoot != "/tmp/file" {
		t.Fatalf("env root = %q", cfg.EnvRoot)
	}
	if cfg.Advertisement.ContractID != "con_inline1234" {
		t.Fatalf("inline contract was overwritten: %q", cfg.Advertisement.ContractID)
	}
	if got := cfg.Advertisement.WorkgroupIDs; len(got) != 1 || got[0] != "wg_abcdefghijkl" {
		t.Fatalf("workgroup IDs were not merged: %#v", got)
	}
}

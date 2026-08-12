package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/openziti/llm-gateway/keys"
)

const keyFileJSON = `{
  "version": 1,
  "keys": [
    {"name": "alice", "key": "sk-gw-alice", "allowed_models": ["claude-*"]},
    {"name": "ci", "key_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}
  ]
}`

func writeKeyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startServer(t *testing.T, cfg config) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newServer(cfg).handler())
	t.Cleanup(server.Close)
	return server
}

// sourceStore wires a real gateway key store to the reference server through
// the exported API, exactly as an operator would. this is the binding test the
// server exists for: it checks the contract through the bytes on the wire
// rather than through a shared struct, which is what a third-party management
// plane actually implements against.
func sourceStore(t *testing.T, baseURL string, token string) (*keys.Store, error) {
	t.Helper()
	required := true
	cfg := &keys.Config{
		Enabled: true,
		Sources: []dd.Dynamic{&keys.HTTPSourceConfig{
			Name:         "reference",
			BaseURL:      baseURL,
			Token:        token,
			PollInterval: time.Minute,
			Timeout:      5 * time.Second,
			Required:     &required,
		}},
	}
	store, err := keys.NewStoreFromConfig(cfg, func(*keys.HTTPSourceConfig) (*http.Client, error) {
		return http.DefaultClient, nil
	})
	if store != nil {
		t.Cleanup(func() { store.Close() })
	}
	return store, err
}

func TestServedKeySetLoadsIntoARealStore(t *testing.T) {
	server := startServer(t, config{path: writeKeyFile(t, keyFileJSON)})

	store, err := sourceStore(t, server.URL, "")
	if err != nil {
		t.Fatalf("NewStoreFromConfig() = %v, want nil", err)
	}

	record, ok := store.Lookup("sk-gw-alice")
	if !ok {
		t.Fatal("expected the served plaintext key to authenticate")
	}
	if record.Name != "alice" {
		t.Errorf("name = %q, want alice", record.Name)
	}
	if !record.AllowsModel("claude-haiku-4-5") || record.AllowsModel("gpt-4") {
		t.Error("allowed_models did not survive the round trip")
	}
	if _, ok := store.Lookup("sk-gw-nobody"); ok {
		t.Error("an unknown key authenticated")
	}
}

func TestBearerTokenIsEnforced(t *testing.T) {
	server := startServer(t, config{path: writeKeyFile(t, keyFileJSON), token: "secret-token"})

	if _, err := sourceStore(t, server.URL, "wrong-token"); err == nil {
		t.Fatal("expected boot to fail when the source presents the wrong token")
	}
	if _, err := sourceStore(t, server.URL, "secret-token"); err != nil {
		t.Fatalf("NewStoreFromConfig() with the right token = %v, want nil", err)
	}
}

// every fault has to be rejected by the gateway's own decoder rather than by an
// assertion written here; a fault the gateway accepts is not a fault.
func TestFaultsAreRejectedByTheGateway(t *testing.T) {
	for _, fault := range []string{
		faultStatus, faultCount, faultPagination,
		faultUnsolicited, faultMalformed, faultUnknownField,
	} {
		t.Run(fault, func(t *testing.T) {
			server := startServer(t, config{path: writeKeyFile(t, keyFileJSON), fault: fault})
			if _, err := sourceStore(t, server.URL, ""); err == nil {
				t.Fatalf("--fault %s was accepted by the gateway", fault)
			}
		})
	}
}

func TestConditionalRequestLifecycle(t *testing.T) {
	server := startServer(t, config{path: writeKeyFile(t, keyFileJSON)})

	first, err := http.Get(server.URL + "/v1/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", first.StatusCode)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag on the first response")
	}

	conditional := func(validator string) int {
		req, _ := http.NewRequest("GET", server.URL+"/v1/keys", nil)
		req.Header.Set("If-None-Match", validator)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := conditional(etag); got != http.StatusNotModified {
		t.Errorf("matching validator = %d, want 304", got)
	}
	if got := conditional(`"stale"`); got != http.StatusOK {
		t.Errorf("stale validator = %d, want 200", got)
	}
	if got := conditional("W/" + etag); got != http.StatusNotModified {
		t.Errorf("weak validator = %d, want 304", got)
	}
}

// the file is re-read per request, so an edit changes both the body and the
// validator without restarting the server.
func TestEditedFileChangesTheValidator(t *testing.T) {
	path := writeKeyFile(t, keyFileJSON)
	server := startServer(t, config{path: path})

	first, err := http.Get(server.URL + "/v1/keys")
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	before := first.Header.Get("ETag")

	if err := os.WriteFile(path, []byte(`{"version":1,"keys":[{"name":"bob","key":"sk-gw-bob"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := http.Get(server.URL + "/v1/keys")
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if after := second.Header.Get("ETag"); after == before {
		t.Fatalf("ETag did not change after the file was edited (%s)", after)
	}
}

func TestReadRecordsAcceptsEnvelopeAndBareList(t *testing.T) {
	envelope := newServer(config{path: writeKeyFile(t, keyFileJSON)})
	records, err := envelope.readRecords()
	if err != nil || len(records) != 2 {
		t.Fatalf("envelope form = %d records, %v; want 2, nil", len(records), err)
	}

	bare := newServer(config{path: writeKeyFile(t, `[{"name":"alice","key":"sk-gw-alice"}]`)})
	records, err = bare.readRecords()
	if err != nil || len(records) != 1 {
		t.Fatalf("bare list form = %d records, %v; want 1, nil", len(records), err)
	}

	broken := newServer(config{path: writeKeyFile(t, `nonsense`)})
	if _, err := broken.readRecords(); err == nil {
		t.Fatal("expected an error for a file that is neither form")
	}
}

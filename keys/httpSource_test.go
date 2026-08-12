package keys

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const validHTTPKeyDocument = `{"version":1,"count":1,"keys":[{"name":"alice","key":"sk-alice"}]}`

func newTestHTTPSource(t *testing.T, server *httptest.Server, token string) *httpSource {
	t.Helper()
	source, err := newHTTPSource(&HTTPSourceConfig{
		Name: "managed", BaseURL: server.URL, Token: token, Timeout: time.Second,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestHTTPSourceConditionalRefreshLifecycle(t *testing.T) {
	const token = "management-token"
	var calls atomic.Int32
	var mu sync.Mutex
	var requests []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, request.Header.Clone())
		mu.Unlock()
		if request.URL.Path != "/v1/keys" {
			t.Errorf("request path = %q, want /v1/keys", request.URL.Path)
		}
		switch calls.Add(1) {
		case 1:
			response.Header().Set("ETag", `"v1"`)
			_, _ = response.Write([]byte(validHTTPKeyDocument))
		case 2:
			response.WriteHeader(http.StatusNotModified)
		case 3:
			response.Header().Set("ETag", `"rejected"`)
			_, _ = response.Write([]byte(`{"version":1,"count":2,"keys":[{"name":"alice","key":"sk-alice"}]}`))
		case 4:
			_, _ = response.Write([]byte(`{"version":1,"count":1,"keys":[{"name":"bob","key":"sk-bob"}]}`))
		case 5:
			response.WriteHeader(http.StatusNotModified)
		default:
			http.Error(response, "unexpected call", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar, _ = cookiejar.New(nil)
	originalTransport := client.Transport
	originalCheckRedirect := reflect.ValueOf(client.CheckRedirect).Pointer()
	originalJar := client.Jar
	originalTimeout := client.Timeout
	source, err := newHTTPSource(&HTTPSourceConfig{
		Name: "managed", BaseURL: server.URL, Token: token, Timeout: 50 * time.Millisecond,
	}, client)
	if err != nil {
		t.Fatal(err)
	}

	result, err := source.Load(t.Context())
	if err != nil || result.IsUnchanged() || result.Contribution().Records[0].Name != "alice" || source.etag != `"v1"` {
		t.Fatalf("first Load() = %#v, %v, etag %q", result, err, source.etag)
	}
	result, err = source.Load(t.Context())
	if err != nil || !result.IsUnchanged() {
		t.Fatalf("conditional Load() = %#v, %v, want unchanged", result, err)
	}
	if _, err := source.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "count is 2") {
		t.Fatalf("rejected Load() = %v, want count mismatch", err)
	}
	if source.etag != `"v1"` {
		t.Fatalf("rejected 200 advanced ETag to %q", source.etag)
	}
	result, err = source.Load(t.Context())
	if err != nil || result.IsUnchanged() || result.Contribution().Records[0].Name != "bob" {
		t.Fatalf("ETag-less Load() = %#v, %v", result, err)
	}
	if source.etag != "" {
		t.Fatalf("accepted ETag-less 200 retained ETag %q", source.etag)
	}
	if _, err := source.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "unconditional") {
		t.Fatalf("unsolicited 304 Load() = %v, want protocol error", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(requests))
	}
	for i, header := range requests {
		if header.Get("Cache-Control") != "no-cache" {
			t.Errorf("request %d Cache-Control = %q", i+1, header.Get("Cache-Control"))
		}
		if header.Get("Authorization") != "Bearer "+token {
			t.Errorf("request %d Authorization was not set", i+1)
		}
	}
	wantValidators := []string{"", `"v1"`, `"v1"`, `"v1"`, ""}
	for i, want := range wantValidators {
		if got := requests[i].Get("If-None-Match"); got != want {
			t.Errorf("request %d If-None-Match = %q, want %q", i+1, got, want)
		}
	}
	if client.Transport != originalTransport || reflect.ValueOf(client.CheckRedirect).Pointer() != originalCheckRedirect ||
		client.Jar != originalJar || client.Timeout != originalTimeout {
		t.Fatal("HTTP source mutated the borrowed client")
	}
}

func TestHTTPSourceRejectsPaginationBeforeStatusDispatch(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header string
		value  string
	}{
		{"Link on 200", http.StatusOK, "Link", `<https://keys/v1/keys?page=2>; rel="next"`},
		{"Content-Range on 200", http.StatusOK, "Content-Range", "items 0-0/2"},
		{"Link on 304", http.StatusNotModified, "Link", `<https://keys/v1/keys?page=2>; rel="next"`},
		{"Content-Range on 304", http.StatusNotModified, "Content-Range", "items 0-0/2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if test.status == http.StatusNotModified && calls.Add(1) == 1 {
					response.Header().Set("ETag", `"v1"`)
					_, _ = response.Write([]byte(validHTTPKeyDocument))
					return
				}
				response.Header().Set(test.header, test.value)
				response.WriteHeader(test.status)
				if test.status == http.StatusOK {
					_, _ = response.Write([]byte(validHTTPKeyDocument))
				}
			}))
			defer server.Close()
			source := newTestHTTPSource(t, server, "")
			if test.status == http.StatusNotModified {
				if _, err := source.Load(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			result, err := source.Load(t.Context())
			if err == nil || !strings.Contains(err.Error(), "pagination") || result.IsUnchanged() {
				t.Fatalf("Load() = %#v, %v, want pagination failure", result, err)
			}
		})
	}
}

func TestHTTPSourceStatusFailuresDoNotExposeSecrets(t *testing.T) {
	logs := captureKeyLogs(t)
	const token = "management-token-sentinel"
	const responseSecret = "response-secret-sentinel"
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		logs.Reset()
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("X-Secret", responseSecret)
			response.WriteHeader(status)
			_, _ = response.Write([]byte(responseSecret))
		}))
		source := newTestHTTPSource(t, server, token)
		_, err := source.Load(t.Context())
		server.Close()
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
			t.Fatalf("Load(status %d) = %v", status, err)
		}
		combined := err.Error() + logs.String()
		if strings.Contains(combined, token) || strings.Contains(combined, responseSecret) {
			t.Fatalf("status %d diagnostics exposed a secret: %q", status, combined)
		}
		if (status == http.StatusUnauthorized || status == http.StatusForbidden) &&
			!strings.Contains(logs.String(), "rejected its credential") {
			t.Fatalf("status %d did not emit the credential-specific log: %q", status, logs.String())
		}
	}
}

func TestHTTPSourceMalformedBodyDoesNotExposeSecrets(t *testing.T) {
	logs := captureKeyLogs(t)
	const token = "management-token-sentinel"
	const keySecret = "response-key-secret-sentinel"
	const rejectedSecret = "rejected-value-secret-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("X-Secret", rejectedSecret)
		_, _ = response.Write([]byte(`{"version":1,"count":1,"keys":[{"name":"alice","key":"` +
			keySecret + `","allowed_models":"` + rejectedSecret + `"}]}`))
	}))
	defer server.Close()

	source := newTestHTTPSource(t, server, token)
	_, err := source.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "keys[0].allowed_models: invalid value") {
		t.Fatalf("Load() = %v, want sanitized record-path error", err)
	}
	combined := err.Error() + logs.String()
	for _, secret := range []string{token, keySecret, rejectedSecret} {
		if strings.Contains(combined, secret) {
			t.Fatalf("malformed-body diagnostics exposed secret %q: %q", secret, combined)
		}
	}
}

func TestHTTPSourceTimeoutDoesNotMutateClient(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar, _ = cookiejar.New(nil)
	originalTransport := client.Transport
	originalCheckRedirect := reflect.ValueOf(client.CheckRedirect).Pointer()
	originalJar := client.Jar
	originalTimeout := client.Timeout
	source, err := newHTTPSource(&HTTPSourceConfig{
		Name: "managed", BaseURL: server.URL, Timeout: 20 * time.Millisecond,
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Load() = %v, want timeout", err)
	}
	<-started
	if client.Transport != originalTransport || reflect.ValueOf(client.CheckRedirect).Pointer() != originalCheckRedirect ||
		client.Jar != originalJar || client.Timeout != originalTimeout {
		t.Fatal("timeout path mutated the borrowed client")
	}
}

func TestHTTPSourceCapsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(strings.Repeat("x", 65)))
	}))
	defer server.Close()
	source := newTestHTTPSource(t, server, "")
	source.maxBodyBytes = 64
	if _, err := source.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "exceeds 64 bytes") {
		t.Fatalf("Load() = %v, want body-size error", err)
	}
}

func TestHTTP304ReinstatesRetainedContribution(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			response.Header().Set("ETag", `"v1"`)
			_, _ = response.Write([]byte(validHTTPKeyDocument))
			return
		}
		if request.Header.Get("If-None-Match") != `"v1"` {
			t.Errorf("If-None-Match = %q, want v1", request.Header.Get("If-None-Match"))
		}
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	cfg := &Config{Enabled: true, Sources: dynamics(&HTTPSourceConfig{
		Name: "managed", BaseURL: server.URL, PollInterval: time.Hour, Timeout: time.Second,
	})}
	store, err := NewStoreFromConfig(cfg, func(*HTTPSourceConfig) (*http.Client, error) {
		return server.Client(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	store.mu.Lock()
	store.states["managed"].excluded = true
	store.recompose()
	store.mu.Unlock()
	if _, ok := store.Lookup("sk-alice"); ok {
		t.Fatal("excluded contribution remained resident")
	}
	store.runners[0].refresh(t.Context())
	if record, ok := store.Lookup("sk-alice"); !ok || record.Name != "alice" {
		t.Fatalf("304 did not reinstate retained contribution: %+v, %v", record, ok)
	}
}

func TestHTTPUnsolicited304FailsRequiredBoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	cfg := &Config{Enabled: true, Sources: dynamics(&HTTPSourceConfig{
		Name: "managed", BaseURL: server.URL, PollInterval: time.Hour, Timeout: time.Second,
	})}
	_, err := NewStoreFromConfig(cfg, func(*HTTPSourceConfig) (*http.Client, error) {
		return server.Client(), nil
	})
	if err == nil || !strings.Contains(err.Error(), "at boot") || !strings.Contains(err.Error(), "unconditional") {
		t.Fatalf("NewStoreFromConfig() = %v, want unsolicited-304 boot error", err)
	}
}

func TestHTTPSourceRequiresInjectedClient(t *testing.T) {
	cfg := &Config{Enabled: true, Sources: dynamics(&HTTPSourceConfig{
		Name: "managed", BaseURL: "https://keys.internal", PollInterval: time.Hour, Timeout: time.Second,
	})}
	if _, err := NewStoreFromConfig(cfg, nil); err == nil || !strings.Contains(err.Error(), "injected HTTP client") {
		t.Fatalf("NewStoreFromConfig() = %v, want client-injection error", err)
	}
}

func TestHasPaginationParsesLinkRelations(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{"next with comma in target", http.Header{"Link": {`<https://keys/items?cursor=a,b>; rel="next"`}}, true},
		{"multiple relations", http.Header{"Link": {`<https://keys/items>; rel="alternate last"`}}, true},
		{"unrelated relation", http.Header{"Link": {`<https://keys/help>; rel="alternate"`}}, false},
		{"content range", http.Header{"Content-Range": {""}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasPagination(test.header); got != test.want {
				t.Fatalf("hasPagination() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHTTPSourceParentCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	source := newTestHTTPSource(t, server, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := source.Load(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Load() = %v, want wrapped context cancellation", err)
	}
}

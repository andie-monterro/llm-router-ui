package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenRouterStripsSelectorPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "moonshotai/kimi-k3" {
			t.Fatalf("model = %q", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","object":"chat.completion","model":"moonshotai/kimi-k3","choices":[]}`))
	}))
	defer server.Close()

	_, err := NewOpenRouter("test-key", server.URL).ChatCompletion(t.Context(), &ChatCompletionRequest{
		Model:    "openrouter/moonshotai/kimi-k3",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

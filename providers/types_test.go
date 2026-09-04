package providers

import (
	"encoding/json"
	"testing"
)

func TestServerToolJSONOmitsFunction(t *testing.T) {
	b, err := json.Marshal(Tool{Type: "openrouter:web_search", Parameters: map[string]any{"engine": "exa"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"type":"openrouter:web_search","parameters":{"engine":"exa"}}`; got != want {
		t.Fatalf("tool JSON = %s, want %s", got, want)
	}
}

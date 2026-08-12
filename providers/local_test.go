package providers

import (
	"net/http"
	"strings"
	"testing"
)

// the local provider fronts any OpenAI-compatible backend (vLLM, SGLang,
// llama-server) as well as ollama's native shape, so parseError has to read
// both envelopes and must never hand an unrecognized body to the client.
func TestLocalParseError(t *testing.T) {
	local := NewLocal("http://localhost:11434")

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantType   string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "openai envelope keeps its type and status",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"bad request","type":"invalid_request_error"}}`,
			wantType:   ErrorTypeInvalidRequest,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "bad request",
		},
		{
			name:       "openai envelope not-found",
			statusCode: http.StatusNotFound,
			body:       `{"error":{"message":"unknown model","type":"not_found_error"}}`,
			wantType:   ErrorTypeNotFound,
			wantStatus: http.StatusNotFound,
			wantMsg:    "unknown model",
		},
		{
			name:       "ollama string form stays a server error",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":"model requires more system memory"}`,
			wantType:   ErrorTypeServer,
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "model requires more system memory",
		},
		{
			name:       "unrecognized body falls back on status alone",
			statusCode: http.StatusBadGateway,
			body:       `<html><body>nginx: upstream unavailable</body></html>`,
			wantType:   ErrorTypeServer,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "unrecognized 404",
			statusCode: http.StatusNotFound,
			body:       `not json`,
			wantType:   ErrorTypeNotFound,
			wantStatus: http.StatusNotFound,
			wantMsg:    "model not found",
		},
		{
			name:       "unrecognized 503",
			statusCode: http.StatusServiceUnavailable,
			body:       `not json`,
			wantType:   ErrorTypeServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := local.parseError(tt.statusCode, []byte(tt.body))
			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("parseError() = %T, want *APIError", err)
			}
			if apiErr.Type != tt.wantType {
				t.Errorf("type = %q, want %q", apiErr.Type, tt.wantType)
			}
			if got := StatusCodeForError(apiErr.Type); got != tt.wantStatus {
				t.Errorf("status = %d, want %d", got, tt.wantStatus)
			}
			if tt.wantMsg != "" && apiErr.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", apiErr.Message, tt.wantMsg)
			}
		})
	}
}

// an unrecognized body belongs to an arbitrary backend reached over an
// operator-configured transport; its contents must not reach the client.
func TestLocalParseErrorDoesNotEmbedUnrecognizedBody(t *testing.T) {
	local := NewLocal("http://localhost:11434")
	body := `<html>proxy debug: internal-host-9 token=s3cret</html>`

	err := local.parseError(http.StatusBadGateway, []byte(body))
	for _, leak := range []string{"internal-host-9", "s3cret", "proxy debug"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("parseError() leaked upstream body content %q: %v", leak, err)
		}
	}
}

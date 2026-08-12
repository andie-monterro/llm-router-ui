package keys

import (
	"context"
	"net/http"
	"strings"

	"github.com/openziti/llm-gateway/providers"
)

type contextKey int

const apiKeyContextKey contextKey = iota

// Middleware returns a handler that enforces bearer-token authentication.
// health and metrics remain unauthenticated as before.
func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			providers.WriteError(w,
				providers.NewAPIError("API key required", providers.ErrorTypeAuthentication),
				http.StatusUnauthorized,
			)
			return
		}

		record, ok := s.Lookup(strings.TrimPrefix(auth, "Bearer "))
		if !ok {
			providers.WriteError(w, providers.ErrUnauthorized, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), apiKeyContextKey, record)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext returns the record bound when the request authenticated.
func FromContext(ctx context.Context) *Record {
	record, _ := ctx.Value(apiKeyContextKey).(*Record)
	return record
}

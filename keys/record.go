package keys

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"time"
)

// Record is the domain form of a key. it carries a digest, never plaintext.
type Record struct {
	Name          string
	Digest        [sha256.Size]byte
	AllowedModels []string
	AllowedRoutes []string
	ExpiresAt     *time.Time
	Source        string
}

// AllowsModel reports whether the record permits a model. an empty restriction
// list permits all models; matching uses path.Match, including its slash rule.
func (r *Record) AllowsModel(model string) bool {
	if len(r.AllowedModels) == 0 {
		return true
	}
	for _, pattern := range r.AllowedModels {
		if matched, _ := path.Match(pattern, model); matched {
			return true
		}
	}
	return false
}

// AllowsRoute reports whether the record permits an exact semantic route.
func (r *Record) AllowsRoute(route string) bool {
	if len(r.AllowedRoutes) == 0 {
		return true
	}
	for _, allowed := range r.AllowedRoutes {
		if allowed == route {
			return true
		}
	}
	return false
}

// Expired reports whether now is at or past the record's expiry instant.
func (r *Record) Expired(now time.Time) bool {
	return r.ExpiresAt != nil && !now.Before(*r.ExpiresAt)
}

// keyGrammar is b64token from the bearer-token specification.
var keyGrammar = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)

// HashKey validates plaintext against the bearer-token grammar and hashes its
// exact bytes without trimming, folding, or normalization.
func HashKey(plaintext string) ([sha256.Size]byte, error) {
	if !keyGrammar.MatchString(plaintext) {
		return [sha256.Size]byte{}, fmt.Errorf("key does not match bearer-token grammar")
	}
	return sha256.Sum256([]byte(plaintext)), nil
}

// ParseDigest parses a SHA-256 digest from exactly 64 hexadecimal characters.
// errors never include the input.
func ParseDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != hex.EncodedLen(len(digest)) {
		return digest, fmt.Errorf("key_sha256 must be 64 hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return digest, fmt.Errorf("key_sha256 must be 64 hexadecimal characters")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func validatePattern(pattern string) error {
	if _, err := path.Match(pattern, ""); err != nil {
		return err
	}
	return nil
}

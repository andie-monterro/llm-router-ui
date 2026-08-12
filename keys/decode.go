package keys

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/michaelquigley/df/dd"
)

var recordPathPattern = regexp.MustCompile(`(?i)keys\[([0-9]+)\](?:\.([a-z0-9_]+)(?:\[([0-9]+)\])?)?`)

var (
	acronymFieldBoundary = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	wordFieldBoundary    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

var nullableFields = []string{"allowed_models", "allowed_routes", "expires_at"}

type wireEnvelope struct {
	Version int          `dd:"version,+required"`
	Count   *int         `dd:"count"`
	Keys    []wireRecord `dd:"keys,+required"`
}

type wireRecord struct {
	Name          string     `dd:"name,+required"`
	Key           *string    `dd:"key"`
	KeySHA256     *string    `dd:"key_sha256"`
	AllowedModels []string   `dd:"allowed_models"`
	AllowedRoutes []string   `dd:"allowed_routes"`
	ExpiresAt     *time.Time `dd:"expires_at"`
}

// StripNullableRecordFields implements the record contract's null carve-out.
// only optional collection and expiry fields are removed; required null values
// continue into strict binding and reject.
func StripNullableRecordFields(records []any) {
	for _, item := range records {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range nullableFields {
			if value, exists := record[field]; exists && value == nil {
				delete(record, field)
			}
		}
	}
}

// SanitizeDecodeError removes decoder text when the failure is beneath a key
// record. decoder errors may render the rejected value, and plaintext key
// material is legal at this boundary; only the structural path is safe.
func SanitizeDecodeError(err error) error {
	return sanitizeDecodeError(err, "api_keys.")
}

func sanitizeSourceDecodeError(err error) error {
	return sanitizeDecodeError(err, "")
}

func sanitizeDecodeError(err error, prefix string) error {
	if err == nil {
		return nil
	}
	if path := decodeErrorPath(err); path != "" {
		if safe := recordPath(path); safe != "" {
			return fmt.Errorf("%s%s: invalid value", prefix, safe)
		}
	}
	if safe := recordPath(err.Error()); safe != "" {
		return fmt.Errorf("%s%s: invalid value", prefix, safe)
	}
	return err
}

func decodeYAMLContribution(data []byte) (*Contribution, error) {
	raw, err := dd.DecodeStrictYAML(data)
	if err != nil {
		if safe := quotedTimestampError(err); safe != nil {
			return nil, safe
		}
		return nil, sanitizeSourceDecodeError(err)
	}
	if records, ok := raw["keys"].([]any); ok {
		StripNullableRecordFields(records)
	}
	if _, exists := raw["count"]; exists {
		return nil, fmt.Errorf("count is not valid in a file key document")
	}

	doc := &wireEnvelope{}
	if err := dd.Bind(doc, raw, dd.Strict()); err != nil {
		return nil, sanitizeSourceDecodeError(err)
	}
	return mapWireEnvelope(doc)
}

func decodeJSONContribution(data []byte) (*Contribution, error) {
	raw, err := dd.DecodeStrictJSON(data)
	if err != nil {
		return nil, sanitizeSourceDecodeError(err)
	}
	if records, ok := raw["keys"].([]any); ok {
		StripNullableRecordFields(records)
	}
	countValue, countPresent := raw["count"]
	if countPresent && countValue == nil {
		// let the rest of the strict envelope bind first so version errors keep
		// their documented precedence, then report null like an absent count.
		delete(raw, "count")
	}
	doc := &wireEnvelope{}
	if err := dd.Bind(doc, raw, dd.Strict()); err != nil {
		return nil, sanitizeSourceDecodeError(err)
	}
	if err := validateWireVersion(doc.Version); err != nil {
		return nil, err
	}
	if !countPresent || countValue == nil || doc.Count == nil {
		return nil, fmt.Errorf("count is required and must not be null")
	}
	if *doc.Count != len(doc.Keys) {
		return nil, fmt.Errorf("count is %d but keys contains %d records", *doc.Count, len(doc.Keys))
	}
	return mapWireEnvelope(doc)
}

func quotedTimestampError(err error) error {
	var conversion *dd.ConversionError
	if !errors.As(err, &conversion) || !strings.HasSuffix(conversion.Path, ".expires_at") ||
		!strings.Contains(conversion.Message, "scalar tag !!timestamp") {
		return nil
	}
	if safe := recordPath(conversion.Path); safe != "" {
		return fmt.Errorf("%s: quote RFC3339 timestamps in YAML", safe)
	}
	return nil
}

func mapWireEnvelope(doc *wireEnvelope) (*Contribution, error) {
	if err := validateWireVersion(doc.Version); err != nil {
		return nil, err
	}

	records := make([]*Record, 0, len(doc.Keys))
	byDigest := make(map[[32]byte]*Record, len(doc.Keys))
	for i := range doc.Keys {
		wire := &doc.Keys[i]
		if wire.Name == "" {
			return nil, fmt.Errorf("keys[%d].name must not be empty", i)
		}
		if wire.Key == nil && wire.KeySHA256 == nil {
			return nil, fmt.Errorf("keys[%d]: exactly one of key or key_sha256 is required", i)
		}
		if wire.Key != nil && wire.KeySHA256 != nil {
			return nil, fmt.Errorf("keys[%d]: key and key_sha256 must not both be present", i)
		}

		var digest [32]byte
		var err error
		switch {
		case wire.Key != nil:
			if *wire.Key == "" {
				return nil, fmt.Errorf("keys[%d].key must not be empty", i)
			}
			digest, err = HashKey(*wire.Key)
			if err != nil {
				return nil, fmt.Errorf("keys[%d].key: invalid bearer-token value", i)
			}
		case wire.KeySHA256 != nil:
			if *wire.KeySHA256 == "" {
				return nil, fmt.Errorf("keys[%d].key_sha256 must not be empty", i)
			}
			digest, err = ParseDigest(*wire.KeySHA256)
			if err != nil {
				return nil, fmt.Errorf("keys[%d].key_sha256: invalid SHA-256 digest", i)
			}
		}

		for j, pattern := range wire.AllowedModels {
			if err := validatePattern(pattern); err != nil {
				return nil, fmt.Errorf("keys[%d].allowed_models[%d]: invalid pattern", i, j)
			}
		}
		for j, route := range wire.AllowedRoutes {
			if route == "" {
				return nil, fmt.Errorf("keys[%d].allowed_routes[%d] must not be empty", i, j)
			}
		}

		record := &Record{
			Name:          wire.Name,
			Digest:        digest,
			AllowedModels: append([]string(nil), wire.AllowedModels...),
			AllowedRoutes: append([]string(nil), wire.AllowedRoutes...),
			ExpiresAt:     wire.ExpiresAt,
		}
		if existing, duplicate := byDigest[digest]; duplicate {
			return nil, fmt.Errorf("keys entries '%s' and '%s' share the same key value", existing.Name, record.Name)
		}
		byDigest[digest] = record
		records = append(records, record)
	}
	return &Contribution{SchemaVersion: doc.Version, Records: records}, nil
}

func validateWireVersion(version int) error {
	if version < 0 {
		return fmt.Errorf("version must not be negative")
	}
	if version != 1 {
		return fmt.Errorf("unsupported key schema version %d", version)
	}
	return nil
}

func decodeErrorPath(err error) string {
	var mismatch *dd.TypeMismatchError
	if errors.As(err, &mismatch) {
		return mismatch.Path
	}
	var conversion *dd.ConversionError
	if errors.As(err, &conversion) {
		return conversion.Path
	}
	var required *dd.RequiredFieldError
	if errors.As(err, &required) {
		return strings.TrimSuffix(required.Path, ".") + "." + required.Field
	}
	var unknown *dd.UnknownFieldError
	if errors.As(err, &unknown) {
		return strings.TrimSuffix(unknown.Path, ".") + "." + unknown.Key
	}
	return ""
}

func recordPath(path string) string {
	match := recordPathPattern.FindStringSubmatch(path)
	if match == nil {
		return ""
	}
	result := "keys[" + match[1] + "]"
	if match[2] != "" {
		result += "." + wireFieldName(match[2])
	}
	if match[3] != "" {
		result += "[" + match[3] + "]"
	}
	return result
}

func wireFieldName(name string) string {
	name = acronymFieldBoundary.ReplaceAllString(name, `${1}_${2}`)
	name = wordFieldBoundary.ReplaceAllString(name, `${1}_${2}`)
	return strings.ToLower(name)
}

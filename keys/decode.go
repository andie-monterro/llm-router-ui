package keys

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/michaelquigley/df/dd"
)

var recordPathPattern = regexp.MustCompile(`(?i)keys\[([0-9]+)\](?:\.([a-z0-9_]+))?`)

var (
	acronymFieldBoundary = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	wordFieldBoundary    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

var nullableFields = []string{"allowed_models", "allowed_routes", "expires_at"}

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
	if err == nil {
		return nil
	}
	if path := decodeErrorPath(err); path != "" {
		if safe := recordPath(path); safe != "" {
			return fmt.Errorf("api_keys.%s: invalid value", safe)
		}
	}
	if safe := recordPath(err.Error()); safe != "" {
		return fmt.Errorf("api_keys.%s: invalid value", safe)
	}
	return err
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
	return result
}

func wireFieldName(name string) string {
	name = acronymFieldBoundary.ReplaceAllString(name, `${1}_${2}`)
	name = wordFieldBoundary.ReplaceAllString(name, `${1}_${2}`)
	return strings.ToLower(name)
}

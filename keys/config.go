package keys

import (
	"fmt"
	"sort"

	"github.com/michaelquigley/df/dd"
)

// Config describes the boot-resident API keys. reloadable sources join this
// subsystem in a later stage; inline keys remain the first contribution.
type Config struct {
	Enabled bool
	Keys    []EntryConfig
	Extra   map[string]any `dd:",+extra"`
}

// EntryConfig is the inline-config wire form of a key. the domain Record is
// produced only after the plaintext key has been expanded and validated.
type EntryConfig struct {
	Name          string         `dd:"name,+required"`
	Key           string         `dd:"key,+required,+secret"`
	AllowedModels []string       `dd:"allowed_models"`
	AllowedRoutes []string       `dd:"allowed_routes"`
	Extra         map[string]any `dd:",+extra"`
}

// BindConfig strict-binds the api_keys subtree after the gateway's otherwise
// forgiving config bind. this keeps exact record spelling local to the key
// contract without changing the acceptance posture of unrelated config.
func BindConfig(raw map[string]any) (*Config, error) {
	if records, ok := raw["keys"].([]any); ok {
		StripNullableRecordFields(records)
	}

	cfg := &Config{}
	if err := dd.Bind(cfg, raw, dd.Strict()); err != nil {
		return nil, SanitizeDecodeError(err)
	}
	if err := rejectExtras("api_keys", cfg.Extra); err != nil {
		return nil, err
	}
	for i := range cfg.Keys {
		if err := rejectExtras(fmt.Sprintf("api_keys.keys[%d]", i), cfg.Keys[i].Extra); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func rejectExtras(path string, extra map[string]any) error {
	if len(extra) == 0 {
		return nil
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Errorf("%s: unknown field '%s'", path, keys[0])
}

package keys

import (
	"fmt"
	"os"
)

// ResolveConfig expands inline key material once at config load. a written
// non-empty value that resolves empty is a directed boot error; an empty value
// stays absent until validation reports it without ever quoting the secret.
func ResolveConfig(cfg *Config) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	for i := range cfg.Keys {
		entry := &cfg.Keys[i]
		field := fmt.Sprintf("api_keys.keys[%d] ('%s')", i, entry.Name)
		if entry.Key == "" {
			return fmt.Errorf("%s has an empty key", field)
		}
		expanded := os.ExpandEnv(entry.Key)
		if expanded == "" {
			return fmt.Errorf("%s key resolves empty (unset environment variable?)", field)
		}
		entry.Key = expanded
	}
	for i, dynamic := range cfg.Sources {
		source, ok := dynamic.(*HTTPSourceConfig)
		if !ok {
			continue
		}
		if err := expandSourceField(i, "base_url", &source.BaseURL); err != nil {
			return err
		}
		if err := expandSourceField(i, "token", &source.Token); err != nil {
			return err
		}
	}
	return nil
}

func expandSourceField(index int, field string, value *string) error {
	if *value == "" {
		return nil
	}
	expanded := os.ExpandEnv(*value)
	if expanded == "" {
		return fmt.Errorf("api_keys.sources[%d].%s resolves empty (unset environment variable?)", index, field)
	}
	*value = expanded
	return nil
}

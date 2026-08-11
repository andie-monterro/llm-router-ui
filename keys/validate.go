package keys

import (
	"fmt"
	"strings"
)

// Validate resolves source identities and rejects configuration the key
// subsystem cannot honor. it runs once after environment expansion.
func Validate(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.Reload != nil && cfg.Reload.MaxStaleness < 0 {
		return fmt.Errorf("api_keys.reload.max_staleness must not be negative")
	}
	if cfg.Reload != nil && cfg.Reload.MaxStaleness > 0 {
		return fmt.Errorf("api_keys.reload.max_staleness only supports 0 until staleness enforcement is enabled")
	}
	if !cfg.Enabled {
		if len(cfg.Sources) > 0 {
			return fmt.Errorf("api_keys.sources requires api_keys.enabled: true")
		}
		return nil
	}
	if len(cfg.Keys) == 0 && len(cfg.Sources) == 0 {
		return fmt.Errorf("api_keys.enabled requires at least one configured key or source")
	}

	names := map[string]struct{}{configSourceName: {}}
	for i, dynamic := range cfg.Sources {
		source, ok := dynamic.(*FileSourceConfig)
		if !ok {
			return fmt.Errorf("api_keys.sources[%d]: unsupported source config %T", i, dynamic)
		}
		name := strings.TrimSpace(source.Name)
		if name == "" {
			name = fmt.Sprintf("%s[%d]", source.Type(), i)
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("api_keys.sources[%d].name '%s' is reserved or duplicated", i, name)
		}
		names[name] = struct{}{}
		source.Name = name

		if source.PollInterval <= 0 {
			return fmt.Errorf("api_keys.sources[%d].poll_interval must be positive", i)
		}
		if strings.TrimSpace(source.Path) == "" {
			return fmt.Errorf("api_keys.sources[%d].path must not be empty", i)
		}
	}
	return nil
}

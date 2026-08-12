package keys

import (
	"fmt"
	"net/url"
	"strings"
	"time"
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
		var name, sourceType string
		var pollInterval, timeout time.Duration
		switch source := dynamic.(type) {
		case *FileSourceConfig:
			name = source.Name
			sourceType = source.Type()
			pollInterval = source.PollInterval
		case *HTTPSourceConfig:
			name = source.Name
			sourceType = source.Type()
			pollInterval = source.PollInterval
			timeout = source.Timeout
		default:
			return fmt.Errorf("api_keys.sources[%d]: unsupported source config %T", i, dynamic)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("%s[%d]", sourceType, i)
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("api_keys.sources[%d].name '%s' is reserved or duplicated", i, name)
		}
		names[name] = struct{}{}
		switch source := dynamic.(type) {
		case *FileSourceConfig:
			source.Name = name
		case *HTTPSourceConfig:
			source.Name = name
		}

		if pollInterval <= 0 {
			return fmt.Errorf("api_keys.sources[%d].poll_interval must be positive", i)
		}
		switch source := dynamic.(type) {
		case *FileSourceConfig:
			if strings.TrimSpace(source.Path) == "" {
				return fmt.Errorf("api_keys.sources[%d].path must not be empty", i)
			}
		case *HTTPSourceConfig:
			if source.Timeout <= 0 {
				return fmt.Errorf("api_keys.sources[%d].timeout must be positive", i)
			}
			parsed, err := url.Parse(source.BaseURL)
			if err != nil || parsed.Host == "" ||
				(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
				return fmt.Errorf("api_keys.sources[%d].base_url must be an absolute HTTP(S) URL", i)
			}
		}
		if timeout > 0 && pollInterval > time.Duration(1<<63-1)-timeout {
			return fmt.Errorf("api_keys.sources[%d].poll_interval plus timeout exceeds the supported duration", i)
		}
		accommodation := pollInterval + timeout
		if cfg.Reload != nil && cfg.Reload.MaxStaleness > 0 && cfg.Reload.MaxStaleness <= accommodation {
			if timeout > 0 {
				return fmt.Errorf("api_keys.reload.max_staleness must be greater than api_keys.sources[%d].poll_interval plus timeout", i)
			}
			return fmt.Errorf("api_keys.reload.max_staleness must be greater than api_keys.sources[%d].poll_interval", i)
		}
	}
	return nil
}

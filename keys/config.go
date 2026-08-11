package keys

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/michaelquigley/df/dd"
)

const (
	defaultPollInterval = 30 * time.Second
	defaultHTTPTimeout  = 5 * time.Second
)

// Config describes inline API keys and their ordered reloadable sources.
type Config struct {
	Enabled bool
	Keys    []EntryConfig
	Sources []dd.Dynamic
	Reload  *ReloadConfig
	Extra   map[string]any `dd:",+extra"`
}

// ReloadConfig carries store-wide reload policy. max staleness is decoded now
// and consumed by the staleness evaluator when that policy is enabled.
type ReloadConfig struct {
	MaxStaleness time.Duration  `dd:"max_staleness"`
	Extra        map[string]any `dd:",+extra"`
}

// FileSourceConfig describes one reloadable YAML key file.
type FileSourceConfig struct {
	Name         string
	Path         string
	Watch        bool
	PollInterval time.Duration `dd:"poll_interval"`
	Required     *bool
	Extra        map[string]any `dd:",+extra"`
}

func (c *FileSourceConfig) Type() string { return "file" }

func (c *FileSourceConfig) ToMap() (map[string]any, error) {
	return dd.Unbind(c)
}

func (c *FileSourceConfig) required() bool {
	return c.Required == nil || *c.Required
}

// HTTPSourceConfig describes one reloadable HTTP key API.
type HTTPSourceConfig struct {
	Name           string
	BaseURL        string        `dd:"base_url"`
	Token          string        `dd:"token,+secret"`
	AgoraTunnel    string        `dd:"agora_tunnel"`
	ZrokShareToken string        `dd:"zrok_share_token,+secret"`
	PollInterval   time.Duration `dd:"poll_interval"`
	Timeout        time.Duration
	Required       *bool
	Extra          map[string]any `dd:",+extra"`
}

func (c *HTTPSourceConfig) Type() string { return "http" }

func (c *HTTPSourceConfig) ToMap() (map[string]any, error) {
	return dd.Unbind(c)
}

func (c *HTTPSourceConfig) required() bool {
	return c.Required == nil || *c.Required
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
	normalizeZeroMaxStaleness(raw)

	cfg := &Config{}
	opts := dd.Strict()
	opts.DynamicBinders = map[string]func(map[string]any) (dd.Dynamic, error){
		"file": bindFileSourceConfig,
		"http": bindHTTPSourceConfig,
	}
	if err := dd.Bind(cfg, raw, opts); err != nil {
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
	if cfg.Reload != nil {
		if err := rejectExtras("api_keys.reload", cfg.Reload.Extra); err != nil {
			return nil, err
		}
	}
	for i, dynamic := range cfg.Sources {
		var extra map[string]any
		switch source := dynamic.(type) {
		case *FileSourceConfig:
			extra = source.Extra
		case *HTTPSourceConfig:
			extra = source.Extra
		default:
			return nil, fmt.Errorf("api_keys.sources[%d]: unsupported source config %T", i, dynamic)
		}
		if err := rejectExtrasExcept(fmt.Sprintf("api_keys.sources[%d]", i), extra, "type"); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func bindFileSourceConfig(raw map[string]any) (dd.Dynamic, error) {
	cfg := &FileSourceConfig{PollInterval: defaultPollInterval}
	if err := dd.Bind(cfg, raw, dd.Strict()); err != nil {
		return nil, err
	}
	return cfg, nil
}

func bindHTTPSourceConfig(raw map[string]any) (dd.Dynamic, error) {
	for _, field := range []string{"token", "zrok_share_token"} {
		if value, exists := raw[field]; exists {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("%s: invalid value", field)
			}
		}
	}
	cfg := &HTTPSourceConfig{PollInterval: defaultPollInterval, Timeout: defaultHTTPTimeout}
	if err := dd.Bind(cfg, raw, dd.Strict()); err != nil {
		return nil, err
	}
	return cfg, nil
}

func normalizeZeroMaxStaleness(raw map[string]any) {
	reload, ok := raw["reload"].(map[string]any)
	if !ok {
		return
	}
	value, exists := reload["max_staleness"]
	if !exists {
		return
	}
	switch value := value.(type) {
	case int:
		if value == 0 {
			reload["max_staleness"] = "0s"
		}
	case int64:
		if value == 0 {
			reload["max_staleness"] = "0s"
		}
	case float64:
		if value == 0 {
			reload["max_staleness"] = "0s"
		}
	case json.Number:
		if value.String() == "0" {
			reload["max_staleness"] = "0s"
		}
	}
}

func rejectExtras(path string, extra map[string]any) error {
	return rejectExtrasExcept(path, extra)
}

func rejectExtrasExcept(path string, extra map[string]any, allowed ...string) error {
	if len(extra) == 0 {
		return nil
	}
	exempt := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		exempt[key] = struct{}{}
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		if _, ok := exempt[key]; ok {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return fmt.Errorf("%s: unknown field '%s'", path, keys[0])
}

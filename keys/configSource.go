package keys

import (
	"context"
	"fmt"
)

const configSourceName = "config"

type configSource struct {
	entries []EntryConfig
}

func newConfigSource(entries []EntryConfig) *configSource {
	return &configSource{entries: entries}
}

func (s *configSource) Name() string {
	return configSourceName
}

func (s *configSource) Load(_ context.Context) (LoadResult, error) {
	records := make([]*Record, 0, len(s.entries))
	byDigest := make(map[[32]byte]*Record, len(s.entries))
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.Name == "" {
			return LoadResult{}, fmt.Errorf("api_keys.keys[%d].name must not be empty", i)
		}
		digest, err := HashKey(entry.Key)
		if err != nil {
			return LoadResult{}, fmt.Errorf("api_keys.keys[%d].key: %w", i, err)
		}
		for j, pattern := range entry.AllowedModels {
			if err := validatePattern(pattern); err != nil {
				return LoadResult{}, fmt.Errorf("api_keys.keys[%d].allowed_models[%d]: invalid pattern", i, j)
			}
		}
		for j, route := range entry.AllowedRoutes {
			if route == "" {
				return LoadResult{}, fmt.Errorf("api_keys.keys[%d].allowed_routes[%d] must not be empty", i, j)
			}
		}

		record := &Record{
			Name:          entry.Name,
			Digest:        digest,
			AllowedModels: append([]string(nil), entry.AllowedModels...),
			AllowedRoutes: append([]string(nil), entry.AllowedRoutes...),
			Source:        configSourceName,
		}
		if existing, duplicate := byDigest[digest]; duplicate {
			return LoadResult{}, fmt.Errorf("api_keys entries '%s' and '%s' share the same key value", existing.Name, record.Name)
		}
		byDigest[digest] = record
		records = append(records, record)
	}
	return Updated(&Contribution{SchemaVersion: 1, Records: records}), nil
}

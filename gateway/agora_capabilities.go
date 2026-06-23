package gateway

import "os"

// capabilityExtras derives llm-gateway agora capabilities from the configured
// providers and routing. It returns only the extras; the base capability
// ("llm-routing") is supplied by the caller via agora.Derive. Ported from the
// first agora pass (origin/agora-v0.1.0:gateway/agora_capabilities.go).
func capabilityExtras(cfg *Config) []string {
	var extras []string
	if cfg == nil {
		return extras
	}

	if cfg.Providers != nil {
		if cfg.Providers.OpenAI != nil && os.ExpandEnv(cfg.Providers.OpenAI.APIKey) != "" {
			extras = append(extras, "openai")
		}
		if cfg.Providers.Anthropic != nil && os.ExpandEnv(cfg.Providers.Anthropic.APIKey) != "" {
			extras = append(extras, "anthropic")
		}
		if cfg.Providers.Local != nil {
			extras = append(extras, "local")
		}
	}

	if cfg.Routing != nil {
		if (cfg.Routing.Semantic != nil && cfg.Routing.Semantic.Enabled) ||
			(cfg.Routing.Classifier != nil && cfg.Routing.Classifier.Enabled) {
			extras = append(extras, "semantic-routing")
		}
	}

	if cfg.AgoraServeEnabled() {
		extras = append(extras, "agora-serve")
	}

	return extras
}

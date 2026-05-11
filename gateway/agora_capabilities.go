package gateway

import "os"

func deriveAgoraCapabilities(cfg *Config) []string {
	capabilities := []string{"llm-routing"}

	if cfg != nil && cfg.Providers != nil {
		if cfg.Providers.OpenAI != nil && os.ExpandEnv(cfg.Providers.OpenAI.APIKey) != "" {
			capabilities = append(capabilities, "openai")
		}
		if cfg.Providers.Anthropic != nil && os.ExpandEnv(cfg.Providers.Anthropic.APIKey) != "" {
			capabilities = append(capabilities, "anthropic")
		}
		if cfg.Providers.Local != nil {
			capabilities = append(capabilities, "local")
		}
	}

	if cfg != nil && cfg.Routing != nil {
		if (cfg.Routing.Semantic != nil && cfg.Routing.Semantic.Enabled) ||
			(cfg.Routing.Classifier != nil && cfg.Routing.Classifier.Enabled) {
			capabilities = append(capabilities, "semantic-routing")
		}
	}

	if cfg != nil && cfg.Agora != nil && cfg.Agora.Serve != nil && cfg.Agora.Serve.Enabled {
		capabilities = append(capabilities, "agora-serve")
	}

	return capabilities
}

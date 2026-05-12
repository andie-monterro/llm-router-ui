package gateway

import (
	"fmt"
	"os"
	"strings"

	"github.com/michaelquigley/df/dd"
)

func loadAgoraIntegrationFile(path string) (*AgoraIntegrationFile, error) {
	file := &AgoraIntegrationFile{}
	if err := dd.MergeYAMLFile(file, path); err != nil {
		return nil, fmt.Errorf("load agora integration file '%s': %w", path, err)
	}
	return file, nil
}

func mergeAgoraIntegrationFile(cfg *AgoraConfig, file *AgoraIntegrationFile) {
	if cfg == nil || file == nil {
		return
	}

	if cfg.APIEndpoint == "" {
		cfg.APIEndpoint = file.APIEndpoint
	}
	if cfg.EnvRoot == "" {
		cfg.EnvRoot = file.EnvRoot
	}
	if file.Advertisement == nil {
		return
	}
	if cfg.Advertisement == nil {
		cfg.Advertisement = &AgoraAdvertisementConfig{}
	}
	if len(cfg.Advertisement.WorkgroupIDs) == 0 {
		cfg.Advertisement.WorkgroupIDs = append([]string(nil), file.Advertisement.WorkgroupIDs...)
	}
	if cfg.Advertisement.ContractID == "" {
		cfg.Advertisement.ContractID = file.Advertisement.ContractID
	}
}

func resolveAgoraConfig(cfg *Config) error {
	if err := normalizeProviderAgoraTunnels(cfg); err != nil {
		return err
	}

	if cfg == nil || cfg.Agora == nil || !cfg.Agora.Enabled {
		return nil
	}

	expandAgoraStrings(cfg.Agora)
	if cfg.Agora.IntegrationFile != "" {
		file, err := loadAgoraIntegrationFile(cfg.Agora.IntegrationFile)
		if err != nil {
			return err
		}
		mergeAgoraIntegrationFile(cfg.Agora, file)
		expandAgoraStrings(cfg.Agora)
	}

	return nil
}

func normalizeProviderAgoraTunnels(cfg *Config) error {
	if cfg == nil || cfg.Providers == nil {
		return nil
	}

	if cfg.Providers.OpenAI != nil {
		cfg.Providers.OpenAI.AgoraTunnel = normalizeProviderAgoraTunnel(cfg.Providers.OpenAI.AgoraTunnel)
	}
	if cfg.Providers.Anthropic != nil {
		cfg.Providers.Anthropic.AgoraTunnel = normalizeProviderAgoraTunnel(cfg.Providers.Anthropic.AgoraTunnel)
	}
	if cfg.Providers.Local != nil {
		cfg.Providers.Local.AgoraTunnel = normalizeProviderAgoraTunnel(cfg.Providers.Local.AgoraTunnel)

		for i := range cfg.Providers.Local.Endpoints {
			cfg.Providers.Local.Endpoints[i].AgoraTunnel = normalizeProviderAgoraTunnel(cfg.Providers.Local.Endpoints[i].AgoraTunnel)
		}
	}

	return nil
}

func normalizeProviderAgoraTunnel(agoraTunnel string) string {
	return strings.TrimSpace(os.ExpandEnv(agoraTunnel))
}

func expandAgoraStrings(cfg *AgoraConfig) {
	cfg.IntegrationFile = os.ExpandEnv(cfg.IntegrationFile)
	cfg.APIEndpoint = os.ExpandEnv(cfg.APIEndpoint)
	cfg.EnvRoot = os.ExpandEnv(cfg.EnvRoot)
	cfg.InstanceName = os.ExpandEnv(cfg.InstanceName)
	cfg.Description = os.ExpandEnv(cfg.Description)
	cfg.TunnelMode = os.ExpandEnv(cfg.TunnelMode)

	if cfg.Advertisement != nil {
		cfg.Advertisement.ContractID = os.ExpandEnv(cfg.Advertisement.ContractID)
		for i := range cfg.Advertisement.WorkgroupIDs {
			cfg.Advertisement.WorkgroupIDs[i] = os.ExpandEnv(cfg.Advertisement.WorkgroupIDs[i])
		}
		for i := range cfg.Advertisement.Capabilities {
			cfg.Advertisement.Capabilities[i] = os.ExpandEnv(cfg.Advertisement.Capabilities[i])
		}
	}
	if cfg.Serve != nil {
		cfg.Serve.BackendTarget = os.ExpandEnv(cfg.Serve.BackendTarget)
		for i := range cfg.Serve.Grants {
			cfg.Serve.Grants[i] = os.ExpandEnv(cfg.Serve.Grants[i])
		}
	}
}

func agoraAdvertisementPublish(cfg *AgoraConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Advertisement != nil && cfg.Advertisement.Publish != nil {
		return *cfg.Advertisement.Publish
	}
	return true
}

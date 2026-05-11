package gateway

import (
	"fmt"
	"strings"
)

const (
	defaultAgoraInstanceName = "llm-gateway"
	defaultAgoraDescription  = "OpenAI-compatible LLM gateway"
	defaultAgoraTunnelMode   = "tcp"
)

type AgoraIdentity struct {
	InstanceName string
	Description  string
	TunnelMode   string
	AgentName    string
}

func resolveAgoraIdentity(cfg *AgoraConfig) (AgoraIdentity, error) {
	if cfg == nil {
		return AgoraIdentity{}, fmt.Errorf("agora config is required")
	}

	instanceName := strings.TrimSpace(cfg.InstanceName)
	if instanceName == "" {
		instanceName = defaultAgoraInstanceName
	}

	description := strings.TrimSpace(cfg.Description)
	if description == "" {
		description = defaultAgoraDescription
	}

	tunnelMode := strings.ToLower(strings.TrimSpace(cfg.TunnelMode))
	if tunnelMode == "" {
		tunnelMode = defaultAgoraTunnelMode
	}
	switch tunnelMode {
	case "tcp", "http", "udp":
	default:
		return AgoraIdentity{}, fmt.Errorf("invalid agora tunnel_mode '%s'", cfg.TunnelMode)
	}

	return AgoraIdentity{
		InstanceName: instanceName,
		Description:  description,
		TunnelMode:   tunnelMode,
		AgentName:    "llm-gateway-" + instanceName,
	}, nil
}

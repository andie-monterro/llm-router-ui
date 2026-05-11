package main

import (
	"testing"

	"github.com/openziti/llm-gateway/gateway"
)

func TestRunCommandApplyOverridesRejectsInvalidNetwork(t *testing.T) {
	rc := &runCommand{network: "invalid"}
	if err := rc.applyOverrides(&gateway.Config{}); err == nil {
		t.Fatal("expected invalid network error")
	}
}

func TestRunCommandApplyOverridesEnablesAgoraNetwork(t *testing.T) {
	rc := &runCommand{network: "agora"}
	cfg := &gateway.Config{}

	if err := rc.applyOverrides(cfg); err != nil {
		t.Fatalf("applyOverrides returned error: %v", err)
	}
	if cfg.Agora == nil || !cfg.Agora.Enabled {
		t.Fatalf("agora was not enabled: %#v", cfg.Agora)
	}
}

func TestRunCommandApplyOverridesAgoraIntegrationFilePrecedence(t *testing.T) {
	t.Setenv("AGORA_LLM_GATEWAY_INTEGRATION_FILE", "/env/agora.yaml")

	cfg := &gateway.Config{}
	if err := (&runCommand{}).applyOverrides(cfg); err != nil {
		t.Fatalf("applyOverrides returned error: %v", err)
	}
	if cfg.Agora == nil || cfg.Agora.IntegrationFile != "/env/agora.yaml" {
		t.Fatalf("env integration file was not applied: %#v", cfg.Agora)
	}

	cfg = &gateway.Config{}
	rc := &runCommand{agoraIntegrationFile: "/flag/agora.yaml"}
	if err := rc.applyOverrides(cfg); err != nil {
		t.Fatalf("applyOverrides returned error: %v", err)
	}
	if cfg.Agora == nil || cfg.Agora.IntegrationFile != "/flag/agora.yaml" {
		t.Fatalf("flag integration file did not win: %#v", cfg.Agora)
	}
}

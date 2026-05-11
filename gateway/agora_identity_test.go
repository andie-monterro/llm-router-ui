package gateway

import "testing"

func TestResolveAgoraIdentityDefaults(t *testing.T) {
	identity, err := resolveAgoraIdentity(&AgoraConfig{})
	if err != nil {
		t.Fatalf("resolveAgoraIdentity returned error: %v", err)
	}
	if identity.InstanceName != "llm-gateway" {
		t.Fatalf("InstanceName = %q", identity.InstanceName)
	}
	if identity.Description != "OpenAI-compatible LLM gateway" {
		t.Fatalf("Description = %q", identity.Description)
	}
	if identity.TunnelMode != "tcp" {
		t.Fatalf("TunnelMode = %q", identity.TunnelMode)
	}
	if identity.AgentName != "llm-gateway-llm-gateway" {
		t.Fatalf("AgentName = %q", identity.AgentName)
	}
}

func TestResolveAgoraIdentityExplicit(t *testing.T) {
	identity, err := resolveAgoraIdentity(&AgoraConfig{
		InstanceName: "engineering",
		Description:  "engineering gateway",
		TunnelMode:   "HTTP",
	})
	if err != nil {
		t.Fatalf("resolveAgoraIdentity returned error: %v", err)
	}
	if identity.InstanceName != "engineering" || identity.Description != "engineering gateway" || identity.TunnelMode != "http" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestResolveAgoraIdentityInvalidMode(t *testing.T) {
	if _, err := resolveAgoraIdentity(&AgoraConfig{TunnelMode: "bogus"}); err == nil {
		t.Fatal("expected invalid tunnel mode error")
	}
}

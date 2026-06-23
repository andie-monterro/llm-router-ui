package agora

// Config holds Agora integration settings for the llm-gateway.
type Config struct {
	Enabled         bool
	IntegrationFile string

	APIEndpoint string
	EnvRoot     string

	InstanceName string
	Description  string

	Advertisement *AdvertisementConfig
	Serve         *ServeConfig
}

// AdvertisementConfig controls Agora catalog publication.
type AdvertisementConfig struct {
	Publish      *bool
	WorkgroupIDs []string `dd:"workgroup_ids"`
	ContractID   string
	Capabilities []string
}

// ServeConfig controls Agora Layer 1 serve behavior. The gateway is bind-only:
// it binds to an operator-provisioned tunnel and never creates or deletes one,
// so there are no serve-side grants here (grants are a provisioning concern and
// confer client/dialer access, not bind permission).
type ServeConfig struct {
	Enabled bool
	// Tunnel is the bind-target tunnel name. The tunnel must already exist as a
	// direct, tcp-mode tunnel that the gateway's account owns; the gateway binds
	// to it and never creates or deletes it. Defaults to InstanceName.
	Tunnel string
}

// IntegrationFile is the demo-bootstrap handoff file shape.
type IntegrationFile struct {
	APIEndpoint   string
	EnvRoot       string
	Advertisement *IntegrationAdvertisement
}

// IntegrationAdvertisement holds catalog identifiers from demo-bootstrap.
type IntegrationAdvertisement struct {
	WorkgroupIDs []string `dd:"workgroup_ids"`
	ContractID   string
}

// ServeEnabled reports whether Agora serving is enabled.
func ServeEnabled(cfg *Config) bool {
	return cfg != nil && cfg.Serve != nil && cfg.Serve.Enabled
}

// AdvertisementPublish reports whether catalog publication is enabled.
func AdvertisementPublish(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.Advertisement != nil && cfg.Advertisement.Publish != nil {
		return *cfg.Advertisement.Publish
	}
	return true
}

// PublishExplicit reports whether advertisement.publish was set explicitly,
// as opposed to publishing being on by default. The gateway uses this to
// distinguish a defaulted publish (silently suppressed for dial-only) from an
// explicit operator request (a directed boot error when serve is off).
func PublishExplicit(cfg *Config) bool {
	return cfg != nil && cfg.Advertisement != nil && cfg.Advertisement.Publish != nil
}

func hasWorkgroupIDs(cfg *Config) bool {
	return cfg != nil && cfg.Advertisement != nil && len(cfg.Advertisement.WorkgroupIDs) > 0
}

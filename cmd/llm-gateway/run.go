package main

import (
	"fmt"
	"os"

	"github.com/michaelquigley/df/dl"
	"github.com/openziti/llm-gateway/gateway"
	"github.com/spf13/cobra"
)

type runCommand struct {
	cmd                  *cobra.Command
	address              string
	zrok                 bool
	zrokMode             string
	network              string
	agoraIntegrationFile string
}

func newRunCommand() *runCommand {
	rc := &runCommand{}
	rc.cmd = &cobra.Command{
		Use:   "run <configPath>",
		Short: "Run the llm-gateway server",
		Args:  cobra.ExactArgs(1),
		RunE:  rc.run,
	}
	rc.cmd.Flags().StringVar(&rc.address, "address", "", "listen address (overrides config)")
	rc.cmd.Flags().BoolVar(&rc.zrok, "zrok", false, "enable zrok sharing (overrides config)")
	rc.cmd.Flags().StringVar(&rc.zrokMode, "zrok-mode", "", "zrok share mode: public, private (overrides config)")
	rc.cmd.Flags().StringVar(&rc.network, "network", "", "network shortcut: zrok or agora")
	rc.cmd.Flags().StringVar(&rc.agoraIntegrationFile, "agora-integration-file", "", "path to Agora integration file (overrides config)")
	return rc
}

func (rc *runCommand) run(_ *cobra.Command, args []string) error {
	configPath := args[0]
	cfg, err := gateway.LoadConfig(configPath)
	if err != nil {
		return err
	}
	dl.Infof("loaded config '%s'", configPath)

	if err := rc.applyOverrides(cfg); err != nil {
		return err
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		return err
	}
	return gw.Run()
}

func (rc *runCommand) applyOverrides(cfg *gateway.Config) error {
	if rc.network != "" && rc.network != "zrok" && rc.network != "agora" {
		return fmt.Errorf("invalid --network value '%s' (expected 'zrok' or 'agora')", rc.network)
	}

	if rc.address != "" {
		cfg.Listen = rc.address
	}
	if rc.zrok {
		if cfg.Zrok == nil {
			cfg.Zrok = &gateway.ZrokConfig{}
		}
		if cfg.Zrok.Share == nil {
			cfg.Zrok.Share = &gateway.ZrokShareConfig{}
		}
		cfg.Zrok.Share.Enabled = true
	}
	if rc.zrokMode != "" {
		if cfg.Zrok == nil {
			cfg.Zrok = &gateway.ZrokConfig{}
		}
		if cfg.Zrok.Share == nil {
			cfg.Zrok.Share = &gateway.ZrokShareConfig{}
		}
		cfg.Zrok.Share.Mode = rc.zrokMode
	}
	if rc.network == "agora" {
		if cfg.Agora == nil {
			cfg.Agora = &gateway.AgoraConfig{}
		}
		cfg.Agora.Enabled = true
	}

	agoraIntegrationFile := rc.agoraIntegrationFile
	if agoraIntegrationFile == "" {
		agoraIntegrationFile = os.Getenv("AGORA_LLM_GATEWAY_INTEGRATION_FILE")
	}
	if agoraIntegrationFile != "" {
		if cfg.Agora == nil {
			cfg.Agora = &gateway.AgoraConfig{}
		}
		cfg.Agora.IntegrationFile = agoraIntegrationFile
	}

	return nil
}

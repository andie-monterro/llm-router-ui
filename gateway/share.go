package gateway

import (
	"fmt"
	"net"

	"github.com/michaelquigley/df/dl"
	"github.com/openziti/zrok/v2/environment"
	"github.com/openziti/zrok/v2/environment/env_core"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

type zrokShareOps interface {
	CreateShare(env_core.Root, *sdk.ShareRequest) (*sdk.Share, error)
	NewListener(string, env_core.Root) (net.Listener, error)
	DeleteShare(env_core.Root, *sdk.Share) error
}

type sdkZrokShareOps struct{}

func (sdkZrokShareOps) CreateShare(root env_core.Root, request *sdk.ShareRequest) (*sdk.Share, error) {
	return sdk.CreateShare(root, request)
}

func (sdkZrokShareOps) NewListener(token string, root env_core.Root) (net.Listener, error) {
	return sdk.NewListener(token, root)
}

func (sdkZrokShareOps) DeleteShare(root env_core.Root, share *sdk.Share) error {
	return sdk.DeleteShare(root, share)
}

var defaultZrokShareOps zrokShareOps = sdkZrokShareOps{}

// Share wraps a zrok share lifecycle.
type Share struct {
	root      env_core.Root
	share     *sdk.Share
	listener  net.Listener
	token     string
	generated bool
	shareOps  zrokShareOps
}

// NewShare creates a zrok share with the specified mode.
// mode can be "public", "private", or empty (defaults to "private").
func NewShare(mode string) (*Share, error) {
	root, err := environment.LoadRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to load zrok environment: %w", err)
	}

	if !root.IsEnabled() {
		return nil, fmt.Errorf("zrok environment is not enabled; run 'zrok enable' first")
	}

	return newShare(root, mode, defaultZrokShareOps)
}

func newShare(root env_core.Root, mode string, shareOps zrokShareOps) (*Share, error) {
	var shareMode sdk.ShareMode
	switch mode {
	case "", "private":
		shareMode = sdk.PrivateShareMode
	case "public":
		shareMode = sdk.PublicShareMode
	default:
		return nil, fmt.Errorf("unknown zrok share mode '%s' (expected 'public' or 'private')", mode)
	}

	dl.Infof("creating zrok %s share", shareMode)

	shareReq := &sdk.ShareRequest{
		BackendMode:    sdk.ProxyBackendMode,
		ShareMode:      shareMode,
		PermissionMode: sdk.OpenPermissionMode,
	}

	shr, err := shareOps.CreateShare(root, shareReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create share: %w", err)
	}

	dl.Infof("created zrok share '%s'", shr.Token)

	listener, err := shareOps.NewListener(shr.Token, root)
	if err != nil {
		if deleteErr := shareOps.DeleteShare(root, shr); deleteErr != nil {
			dl.Errorf("failed to delete share after listener failure: %v", deleteErr)
		}
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	dl.Infof("listener ready for share '%s'", shr.Token)

	return &Share{
		root:      root,
		share:     shr,
		listener:  listener,
		token:     shr.Token,
		generated: true,
		shareOps:  shareOps,
	}, nil
}

// NewShareFromToken creates a Share from an existing persistent share token.
// persistent shares are private shares that persist across restarts; the share
// won't be deleted on Close since it's managed externally.
func NewShareFromToken(token string) (*Share, error) {
	root, err := environment.LoadRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to load zrok environment: %w", err)
	}

	if !root.IsEnabled() {
		return nil, fmt.Errorf("zrok environment is not enabled; run 'zrok enable' first")
	}

	return newShareFromToken(root, token, defaultZrokShareOps)
}

func newShareFromToken(root env_core.Root, token string, shareOps zrokShareOps) (*Share, error) {
	dl.Info("connecting to existing persistent zrok share")

	listener, err := shareOps.NewListener(token, root)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener for persistent zrok share: %w", err)
	}

	dl.Info("listener ready for persistent zrok share")

	return &Share{
		root:      root,
		listener:  listener,
		token:     token,
		generated: false,
		shareOps:  shareOps,
	}, nil
}

// GeneratedToken returns the share token only when this process created it.
func (s *Share) GeneratedToken() (string, bool) {
	if !s.generated {
		return "", false
	}
	return s.token, true
}

// Listener returns the net.Listener for serving HTTP.
func (s *Share) Listener() net.Listener {
	return s.listener
}

// Close terminates the share and cleans up resources.
// for supplied persistent shares, only the listener is closed.
func (s *Share) Close() error {
	var lastErr error

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			dl.Errorf("error closing listener: %v", err)
			lastErr = err
		}
	}

	// delete the share only if this process created it.
	if s.generated && s.share != nil && s.root != nil && s.shareOps != nil {
		if err := s.shareOps.DeleteShare(s.root, s.share); err != nil {
			dl.Errorf("error deleting share: %v", err)
			lastErr = err
		}
	}

	if s.generated {
		dl.Infof("share '%s' closed", s.token)
	} else {
		dl.Info("persistent zrok share closed")
	}
	return lastErr
}

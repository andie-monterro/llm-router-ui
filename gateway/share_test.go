package gateway

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/openziti/zrok/v2/environment/env_core"
	"github.com/openziti/zrok/v2/sdk/golang/sdk"
)

type fakeZrokShareOps struct {
	createdToken string
	listenerErr  error
	listener     *fakeShareListener
	deleted      int
}

func (f *fakeZrokShareOps) CreateShare(env_core.Root, *sdk.ShareRequest) (*sdk.Share, error) {
	return &sdk.Share{Token: f.createdToken}, nil
}

func (f *fakeZrokShareOps) NewListener(string, env_core.Root) (net.Listener, error) {
	if f.listenerErr != nil {
		return nil, f.listenerErr
	}
	if f.listener == nil {
		f.listener = &fakeShareListener{}
	}
	return f.listener, nil
}

func (f *fakeZrokShareOps) DeleteShare(env_core.Root, *sdk.Share) error {
	f.deleted++
	return nil
}

type fakeShareListener struct {
	closed int
}

func (*fakeShareListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (f *fakeShareListener) Close() error {
	f.closed++
	return nil
}
func (*fakeShareListener) Addr() net.Addr { return fakeShareAddr("zrok") }

type fakeShareAddr string

func (a fakeShareAddr) Network() string { return string(a) }
func (a fakeShareAddr) String() string  { return string(a) }

func TestPersistentShareLifecycleRedactsSuppliedToken(t *testing.T) {
	logs := captureGatewayLogs(t)
	const token = "persistent-share-token-sentinel"
	ops := &fakeZrokShareOps{}

	share, err := newShareFromToken(nil, token, ops)
	if err != nil {
		t.Fatal(err)
	}
	if generatedToken, generated := share.GeneratedToken(); generated || generatedToken != "" {
		t.Fatalf("GeneratedToken() = %q, %v for supplied token", generatedToken, generated)
	}
	logZrokServing(share)
	if err := share.Close(); err != nil {
		t.Fatal(err)
	}

	output := logs.String()
	if strings.Contains(output, token) || !strings.Contains(output, "persistent zrok share") {
		t.Fatalf("persistent share lifecycle logs = %q", output)
	}
	if ops.listener.closed != 1 {
		t.Fatalf("listener closes = %d, want 1", ops.listener.closed)
	}
}

func TestPersistentShareListenerFailureRedactsSuppliedToken(t *testing.T) {
	logs := captureGatewayLogs(t)
	const token = "failed-persistent-share-token-sentinel"
	ops := &fakeZrokShareOps{listenerErr: errors.New("listener failed")}

	share, err := newShareFromToken(nil, token, ops)
	if err == nil || share != nil {
		t.Fatalf("newShareFromToken() = %#v, %v, want failure", share, err)
	}
	if output := logs.String() + err.Error(); strings.Contains(output, token) {
		t.Fatalf("persistent share failure exposed token: %q", output)
	}
}

func TestGeneratedShareTokenRemainsOperatorOutput(t *testing.T) {
	logs := captureGatewayLogs(t)
	const token = "generated-share-token-sentinel"
	ops := &fakeZrokShareOps{createdToken: token}

	share, err := newShare(nil, "private", ops)
	if err != nil {
		t.Fatal(err)
	}
	if generatedToken, generated := share.GeneratedToken(); !generated || generatedToken != token {
		t.Fatalf("GeneratedToken() = %q, %v", generatedToken, generated)
	}
	logZrokServing(share)
	if err := share.Close(); err != nil {
		t.Fatal(err)
	}
	if output := logs.String(); !strings.Contains(output, token) {
		t.Fatalf("generated share token missing from operator output: %q", output)
	}
}

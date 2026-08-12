//go:build !windows

package gateway

import (
	"os"
	"syscall"
)

func reloadSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}

func isReloadSignal(signal os.Signal) bool {
	return signal == syscall.SIGHUP
}

//go:build windows

package gateway

import "os"

func reloadSignals() []os.Signal {
	return nil
}

func isReloadSignal(os.Signal) bool {
	return false
}

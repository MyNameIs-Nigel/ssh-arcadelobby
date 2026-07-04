package server

import (
	"context"
	"sync"
)

// ShutdownHook is called during graceful shutdown before the SSH server stops.
type ShutdownHook func(ctx context.Context) error

var (
	shutdownMu sync.Mutex
	shutdowns  []ShutdownHook
)

// RegisterShutdownHook adds a hook invoked on SIGINT/SIGTERM.
func RegisterShutdownHook(h ShutdownHook) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	shutdowns = append(shutdowns, h)
}

// RunShutdownHooks executes registered hooks in registration order.
func RunShutdownHooks(ctx context.Context) error {
	shutdownMu.Lock()
	hooks := append([]ShutdownHook(nil), shutdowns...)
	shutdownMu.Unlock()

	for _, h := range hooks {
		if err := h(ctx); err != nil {
			return err
		}
	}
	return nil
}

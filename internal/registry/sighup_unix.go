//go:build !windows

package registry

import (
	"os"
	"os/signal"
	"syscall"
)

// watchSighup invokes fn on SIGHUP until the returned stop func is called.
func watchSighup(fn func()) (stop func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-quit:
				return
			case <-c:
				fn()
			}
		}
	}()
	return func() {
		signal.Stop(c)
		close(quit)
	}
}

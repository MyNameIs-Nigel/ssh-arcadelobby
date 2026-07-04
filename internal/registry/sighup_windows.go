//go:build windows

package registry

// watchSighup is a no-op on Windows dev hosts; there is no SIGHUP.
func watchSighup(func()) (stop func()) {
	return func() {}
}

//go:build !releasecheck

package version

import "testing"

func TestBuildVersion(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("built version = %q, want dev", Version)
	}
}

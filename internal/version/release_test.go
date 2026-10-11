//go:build releasecheck

package version

import "testing"

func TestBuildVersion(t *testing.T) {
	if Version != "2.4.3" {
		t.Fatalf("built version = %q, want 2.4.3", Version)
	}
}

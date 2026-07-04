package registry

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
)

func registryFor(t *testing.T, addrs map[string]string) *Registry {
	t.Helper()
	var toml string
	order := 0
	for id, addr := range addrs {
		order += 10
		toml += fmt.Sprintf("[[games]]\nid = %q\nname = %q\naddr = %q\norder = %d\n\n",
			id, id, addr, order)
	}
	path := filepath.Join(t.TempDir(), "games.toml")
	writeGames(t, path, toml)
	r, err := New(path, Options{
		DialTimeout: 300 * time.Millisecond,
		Logger:      quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func status(t *testing.T, r *Registry, id string) GameStatus {
	t.Helper()
	for _, g := range r.Games() {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("game %s not in registry", id)
	return GameStatus{}
}

// TestProbeBannerListenerGoesOnline: a listener that speaks an SSH banner
// probes Online after a single successful cycle.
func TestProbeBannerListenerGoesOnline(t *testing.T) {
	addr := testutil.StartBannerListener(t)
	r := registryFor(t, map[string]string{"game": addr})
	sub := r.Subscribe()

	r.probeCycle()
	if !status(t, r, "game").Online {
		t.Fatal("banner listener not Online after one success")
	}
	select {
	case <-sub:
	default:
		t.Fatal("no poke on status change")
	}
}

// TestProbeSilentListenerStaysOffline: accepting the TCP connection is not
// enough — a hung container that never speaks SSH counts as down.
func TestProbeSilentListenerStaysOffline(t *testing.T) {
	addr := testutil.StartSilentListener(t)
	r := registryFor(t, map[string]string{"game": addr})

	r.probeCycle()
	r.probeCycle()
	r.probeCycle()
	if status(t, r, "game").Online {
		t.Fatal("silent listener probed Online")
	}
}

func TestProbeClosedPortStaysOffline(t *testing.T) {
	// Grab a port and close it so nothing listens there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	r := registryFor(t, map[string]string{"game": addr})
	r.probeCycle()
	r.probeCycle()
	if status(t, r, "game").Online {
		t.Fatal("closed port probed Online")
	}
}

// TestFlapDamping: Offline needs two consecutive failures; one blip keeps
// the game Online.
func TestFlapDamping(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = fmt.Fprint(conn, "SSH-2.0-flaptest\r\n")
			_ = conn.Close()
		}
	}()

	r := registryFor(t, map[string]string{"game": addr})
	r.probeCycle()
	if !status(t, r, "game").Online {
		t.Fatal("not Online after success")
	}

	_ = ln.Close()
	r.probeCycle()
	if !status(t, r, "game").Online {
		t.Fatal("went Offline after a single failure — no flap damping")
	}
	r.probeCycle()
	if status(t, r, "game").Online {
		t.Fatal("still Online after two consecutive failures")
	}
}

// TestReportFlipsOfflineImmediately: a bridge dial failure must not wait
// out the damping threshold.
func TestReportFlipsOfflineImmediately(t *testing.T) {
	addr := testutil.StartBannerListener(t)
	r := registryFor(t, map[string]string{"game": addr})
	r.probeCycle()
	if !status(t, r, "game").Online {
		t.Fatal("setup: game should be Online")
	}
	sub := r.Subscribe()

	r.Report("game", fmt.Errorf("dial failed"))
	if status(t, r, "game").Online {
		t.Fatal("Report did not flip the game Offline")
	}
	select {
	case <-sub:
	default:
		t.Fatal("no poke after Report")
	}

	// Reporting an unknown game must not panic or notify.
	r.Report("nope", fmt.Errorf("x"))
}

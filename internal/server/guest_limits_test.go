package server

import (
	"context"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/identity"
	"net"
	"testing"
	"time"
)

func TestGuestLimitsReserveKeyedCapacityAndRelease(t *testing.T) {
	l := NewSessionLimits(4, 2, 2, 1)
	a, b := identity.NewGuest(), identity.NewGuest()
	if !l.acquire(a, "127.0.0.1") || l.acquire(b, "127.0.0.1") || !l.acquire(b, "::1") || l.acquire(identity.NewGuest(), "elsewhere") {
		t.Fatal("guest caps not enforced")
	}
	keyed := identity.NewKeyed(testSigner(t).PublicKey(), "me")
	if !l.acquire(keyed, "") {
		t.Fatal("guest budget starved keyed player")
	}
	l.release(a, "127.0.0.1")
	l.release(b, "::1")
	l.release(keyed, "")
	if l.global != 0 || l.guests != 0 || len(l.perIP) != 0 || len(l.perKey) != 0 {
		t.Fatal("caps leaked")
	}
	if l.acquire(identity.Identity{}, "x") {
		t.Fatal("invalid identity admitted")
	}
}
func TestPendingAuthDeadlineCapacityAndSuccess(t *testing.T) {
	g := newAuthGuard(1, 3, 40*time.Millisecond)
	a, b := net.Pipe()
	defer b.Close()
	c := g.wrap(a)
	if c == nil {
		t.Fatal("first connection refused")
	}
	defer c.Close()
	x, y := net.Pipe()
	defer y.Close()
	defer x.Close()
	if g.wrap(x) != nil {
		t.Fatal("pending cap bypassed")
	}
	// The SSH library clears deadlines while reading; guard must clamp it.
	_ = c.SetDeadline(time.Time{})
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("stalled handshake was unbounded")
	}
	c.authenticated()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 1)); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("deadline survived authentication: %v", err)
	case <-ctx.Done():
	}
	_ = c.Close()
	<-done
	if len(g.pending) != 0 || len(g.connections) != 0 {
		t.Fatal("transport counters leaked")
	}
}

func TestGuestTransportBudgetAndCanonicalIPs(t *testing.T) {
	g := newAuthGuard(8, 10, time.Second)
	g.maxGuests = 1
	g.maxGuestsPerIP = 1
	a, b := net.Pipe()
	defer b.Close()
	c := g.wrap(a)
	x, y := net.Pipe()
	defer y.Close()
	d := g.wrap(x)
	defer d.Close()
	if !c.admitGuest() || d.admitGuest() {
		t.Fatal("guest transport budget bypassed")
	}
	c.authenticated()
	_ = c.Close()
	if !d.admitGuest() {
		t.Fatal("guest transport budget leaked")
	}
	_ = d.Close()
	_ = d.Close()
	if len(g.guestConnections) != 0 || len(g.guestIPs) != 0 {
		t.Fatal("guest IP buckets leaked")
	}
	ipv4 := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1000}
	ipv6 := &net.TCPAddr{IP: net.ParseIP("::1"), Port: 2000}
	if clientIP(ipv4) != "127.0.0.1" || clientIP(ipv6) != "::1" {
		t.Fatal("client IP includes source port")
	}
	g.closePending()
	q, r := net.Pipe()
	defer q.Close()
	defer r.Close()
	if g.wrap(q) != nil {
		t.Fatal("shutdown admitted pending handshake")
	}
}

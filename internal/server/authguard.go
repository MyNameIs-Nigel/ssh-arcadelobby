package server

import (
	"net"
	"sync"
	"time"
)

type authGuard struct {
	closed                     bool
	guestConnections           map[*guardedConn]string
	guestIPs                   map[string]int
	maxGuests, maxGuestsPerIP  int
	mu                         sync.Mutex
	pending                    map[*guardedConn]struct{}
	connections                map[*guardedConn]struct{}
	maxPending, maxConnections int
	timeout                    time.Duration
}

func newAuthGuard(pending, connections int, timeout time.Duration) *authGuard {
	return &authGuard{pending: make(map[*guardedConn]struct{}), connections: make(map[*guardedConn]struct{}), maxPending: pending, maxConnections: connections, timeout: timeout, guestConnections: make(map[*guardedConn]string), guestIPs: make(map[string]int), maxGuests: connections, maxGuestsPerIP: connections}
}
func (g *authGuard) wrap(c net.Conn) *guardedConn {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || len(g.pending) >= g.maxPending || len(g.connections) >= g.maxConnections {
		return nil
	}
	conn := &guardedConn{Conn: c, guard: g, authDeadline: time.Now().Add(g.timeout)}
	g.pending[conn] = struct{}{}
	g.connections[conn] = struct{}{}
	_ = conn.SetDeadline(conn.authDeadline)
	return conn
}
func (g *authGuard) closePending() {
	g.mu.Lock()
	g.closed = true
	pending := make([]*guardedConn, 0, len(g.pending))
	for c := range g.pending {
		pending = append(pending, c)
	}
	g.mu.Unlock()
	for _, c := range pending {
		_ = c.Close()
	}
}

type guardedConn struct {
	net.Conn
	guard        *authGuard
	once         sync.Once
	deadlineMu   sync.Mutex
	ready        bool
	authDeadline time.Time
}

// Charm SSH updates deadlines on every read/write. Clamp its zero deadline
// until authentication actually succeeds, then remove only our auth deadline.
func (c *guardedConn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if !c.ready && (t.IsZero() || t.After(c.authDeadline)) {
		t = c.authDeadline
	}
	return c.Conn.SetDeadline(t)
}
func (c *guardedConn) authenticated() {
	c.deadlineMu.Lock()
	c.ready = true
	_ = c.Conn.SetDeadline(time.Time{})
	c.deadlineMu.Unlock()
	c.guard.mu.Lock()
	delete(c.guard.pending, c)
	c.guard.mu.Unlock()
}
func (c *guardedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.guard.mu.Lock()
		delete(c.guard.pending, c)
		delete(c.guard.connections, c)
		if ip, ok := c.guard.guestConnections[c]; ok {
			delete(c.guard.guestConnections, c)
			c.guard.guestIPs[ip]--
			if c.guard.guestIPs[ip] == 0 {
				delete(c.guard.guestIPs, ip)
			}
		}
		c.guard.mu.Unlock()
	})
	return err
}

type authConnContextKey struct{}

// Reserve guest transports too: authenticating without opening a shell must
// not consume all connection slots reserved for keyed clients.
func (c *guardedConn) admitGuest() bool {
	g := c.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	if _, ok := g.connections[c]; !ok {
		return false
	}
	if _, ok := g.guestConnections[c]; ok {
		return true
	}
	ip := clientIP(c.RemoteAddr())
	if len(g.guestConnections) >= g.maxGuests || g.guestIPs[ip] >= g.maxGuestsPerIP {
		return false
	}
	g.guestConnections[c] = ip
	g.guestIPs[ip]++
	return true
}

package server

import (
	"fmt"
	"github.com/charmbracelet/ssh"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/identity"
	"io"
	"net"
	"sync"
)

// SessionLimits reserves part of the global budget for keyed players and
// limits guests by their public client IP, never by the proxy or username.
type SessionLimits struct {
	mu                                        sync.Mutex
	global, guests                            int
	perKey, perIP                             map[string]int
	maxGlobal, maxPerKey, maxGuests, maxPerIP int
}

func NewSessionLimits(global, perKey, guests, perIP int) *SessionLimits {
	return &SessionLimits{perKey: make(map[string]int), perIP: make(map[string]int), maxGlobal: global, maxPerKey: perKey, maxGuests: guests, maxPerIP: perIP}
}
func (l *SessionLimits) Middleware() func(ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			id := sessionIdentity(s)
			ip := clientIP(s.RemoteAddr())
			if !l.acquire(id, ip) {
				_, _ = io.WriteString(s, fmt.Sprintf("Too many active sessions (global max %d, per-key max %d; guest max %d, per-IP max %d). Try again later.\r\n", l.maxGlobal, l.maxPerKey, l.maxGuests, l.maxPerIP))
				_ = s.Exit(1)
				return
			}
			defer l.release(id, ip)
			next(s)
		}
	}
}
func clientIP(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}
func (l *SessionLimits) acquire(id identity.Identity, ip string) bool {
	if id.Validate() != nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global >= l.maxGlobal {
		return false
	}
	if id.Kind == identity.Guest {
		if l.guests >= l.maxGuests || l.perIP[ip] >= l.maxPerIP {
			return false
		}
		l.guests++
		l.perIP[ip]++
	} else {
		if l.perKey[id.Fingerprint] >= l.maxPerKey {
			return false
		}
		l.perKey[id.Fingerprint]++
	}
	l.global++
	return true
}
func (l *SessionLimits) release(id identity.Identity, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id.Kind == identity.Guest {
		if l.perIP[ip] <= 0 {
			return
		}
		l.guests--
		l.perIP[ip]--
		if l.perIP[ip] == 0 {
			delete(l.perIP, ip)
		}
	} else {
		if l.perKey[id.Fingerprint] <= 0 {
			return
		}
		l.perKey[id.Fingerprint]--
		if l.perKey[id.Fingerprint] == 0 {
			delete(l.perKey, id.Fingerprint)
		}
	}
	l.global--
}

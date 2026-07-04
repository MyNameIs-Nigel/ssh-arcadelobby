package server

import (
	"fmt"
	"io"
	"sync"

	"github.com/charmbracelet/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
)

// SessionLimits tracks global and per-key concurrent sessions.
type SessionLimits struct {
	mu        sync.Mutex
	global    int
	perKey    map[string]int
	maxGlobal int
	maxPerKey int
}

// NewSessionLimits creates a limiter with the given caps.
func NewSessionLimits(maxGlobal, maxPerKey int) *SessionLimits {
	return &SessionLimits{
		perKey:    make(map[string]int),
		maxGlobal: maxGlobal,
		maxPerKey: maxPerKey,
	}
}

// Middleware rejects connections when caps are exceeded.
func (l *SessionLimits) Middleware() func(ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			key := s.PublicKey()
			if key == nil {
				_, _ = io.WriteString(s, "Public key authentication is required.\r\n")
				s.Exit(1)
				return
			}
			fp := proxyproto.Fingerprint(key)
			if !l.acquire(fp) {
				_, _ = io.WriteString(s, fmt.Sprintf(
					"Too many active sessions (global max %d, per-key max %d). Try again later.\r\n",
					l.maxGlobal, l.maxPerKey,
				))
				s.Exit(1)
				return
			}
			defer l.release(fp)
			next(s)
		}
	}
}

func (l *SessionLimits) acquire(fingerprint string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.global >= l.maxGlobal {
		return false
	}
	if l.perKey[fingerprint] >= l.maxPerKey {
		return false
	}

	l.global++
	l.perKey[fingerprint]++
	return true
}

func (l *SessionLimits) release(fingerprint string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.global--
	if l.perKey[fingerprint] > 0 {
		l.perKey[fingerprint]--
		if l.perKey[fingerprint] == 0 {
			delete(l.perKey, fingerprint)
		}
	}
}

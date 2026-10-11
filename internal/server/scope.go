package server

import (
	"sync"

	"github.com/charmbracelet/ssh"
)

// A transport context is shared by SSH channels. Lobby identity and preference
// state belong to one shell channel and must never overwrite another's.
type shellContext struct {
	ssh.Context
	values sync.Map
}

func (c *shellContext) Value(key any) any {
	if v, ok := c.values.Load(key); ok {
		return v
	}
	return c.Context.Value(key)
}
func (c *shellContext) SetValue(key, value any) { c.values.Store(key, value) }

type shellSession struct {
	ssh.Session
	ctx ssh.Context
}

func (s shellSession) Context() ssh.Context { return s.ctx }
func scopeSession() func(ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) { next(shellSession{Session: s, ctx: &shellContext{Context: s.Context()}}) }
	}
}

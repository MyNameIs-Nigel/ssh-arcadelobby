package server

import (
	"context"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/store"
)

type sessionPrefs struct {
	store       *store.Store
	fingerprint string
	now         func() int64
}

func (p sessionPrefs) HasAlphaAck(gameID string) bool {
	if p.store == nil {
		return false
	}
	ok, err := p.store.HasAlphaAck(context.Background(), p.fingerprint, gameID)
	return err == nil && ok
}

func (p sessionPrefs) AckAlpha(gameID string) error {
	if p.store == nil {
		return nil
	}
	now := int64(0)
	if p.now != nil {
		now = p.now()
	}
	return p.store.AckAlphaWarning(context.Background(), p.fingerprint, gameID, now)
}

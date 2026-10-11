package server

import (
	"context"
)

type accountStore interface {
	TouchAccount(context.Context, string, string, int64) error
	HasAlphaAck(context.Context, string, string) (bool, error)
	AckAlphaWarning(context.Context, string, string, int64) error
}
type memoryPrefs map[string]bool

func (p memoryPrefs) HasAlphaAck(gameID string) bool { return p[gameID] }
func (p memoryPrefs) AckAlpha(gameID string) error   { p[gameID] = true; return nil }

type sessionPrefs struct {
	store       accountStore
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

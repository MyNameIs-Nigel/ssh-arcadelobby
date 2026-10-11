// Package identity represents an authenticated lobby shell. Guest IDs are
// ephemeral correlation tokens, never fingerprints or database keys.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
	gossh "golang.org/x/crypto/ssh"
)

type Kind uint8

const (
	Invalid Kind = iota
	Keyed
	Guest
)

type Identity struct {
	Kind                       Kind
	Fingerprint, Slot, GuestID string
	PublicKey                  gossh.PublicKey
}

func NewKeyed(key gossh.PublicKey, user string) Identity {
	slot := proxyproto.SanitizeSlot(user)
	if slot == "" {
		slot = proxyproto.DefaultSlot
	}
	return Identity{Kind: Keyed, Fingerprint: proxyproto.Fingerprint(key), Slot: slot, PublicKey: key}
}
func NewGuest() Identity { return Identity{Kind: Guest, GuestID: NewToken()} }
func NewToken() string   { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func ValidToken(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
func (id Identity) Validate() error {
	switch id.Kind {
	case Keyed:
		if id.PublicKey != nil && id.Fingerprint == proxyproto.Fingerprint(id.PublicKey) && id.GuestID == "" && id.Slot != "" && id.Slot == proxyproto.SanitizeSlot(id.Slot) {
			return nil
		}
	case Guest:
		if ValidToken(id.GuestID) && id.PublicKey == nil && id.Fingerprint == "" && id.Slot == "" {
			return nil
		}
	}
	return fmt.Errorf("invalid lobby identity")
}
func (id Identity) PresenceKey() string {
	if id.Kind == Guest {
		return "guest:" + id.GuestID
	}
	return "key:" + id.Fingerprint
}

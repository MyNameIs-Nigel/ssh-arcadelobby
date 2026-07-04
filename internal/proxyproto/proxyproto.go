// Package proxyproto implements the router side of the ssharcade
// trusted-proxy identity protocol. The canonical contract is
// docs/02-bridge-and-identity-protocol.md; game repos vendor their own copy
// of Parse and cite that document by path. Any change to the username
// encoding or env vars below bumps Version and updates every game repo in
// the same change set.
package proxyproto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	gossh "golang.org/x/crypto/ssh"
)

// Version is the protocol version this package implements.
const Version = 1

// EnvPlayerKey is the env request sent after the session channel opens: the
// player's public key in authorized_keys format. Games may store it; they
// must not require it, and must ignore it on non-proxied connections.
const EnvPlayerKey = "ARCADE_PLAYER_KEY"

// DefaultSlot is sent when the player's SSH username sanitizes to empty.
const DefaultSlot = "default"

const (
	fpHexLen   = 64
	maxSlotLen = 32
)

// Encode builds the proxied username "<fp-hex64>.<slot>": lowercase hex of
// sha256(playerPublicKey.Marshal()), one dot, the sanitized save slot. The
// slot is passed through SanitizeSlot; if nothing valid remains the router
// sends DefaultSlot.
func Encode(fp [sha256.Size]byte, slot string) string {
	s := SanitizeSlot(slot)
	if s == "" {
		s = DefaultSlot
	}
	return hex.EncodeToString(fp[:]) + "." + s
}

// Parse strictly decodes a proxied username. Game-side rule: anything that
// does not parse on a proxied connection is a protocol error — refuse the
// session, never fall back to treating the proxy key as a player account.
func Parse(username string) (fp [sha256.Size]byte, slot string, err error) {
	dot := strings.IndexByte(username, '.')
	if dot != fpHexLen {
		return fp, "", fmt.Errorf("proxyproto: username %q: want %d hex chars then a dot", username, fpHexLen)
	}
	hexPart := username[:fpHexLen]
	for i := 0; i < len(hexPart); i++ {
		c := hexPart[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fp, "", fmt.Errorf("proxyproto: username fingerprint is not lowercase hex")
		}
	}
	raw, err := hex.DecodeString(hexPart)
	if err != nil {
		return fp, "", fmt.Errorf("proxyproto: decode fingerprint: %w", err)
	}
	copy(fp[:], raw)

	slot = username[fpHexLen+1:]
	if !validSlot(slot) {
		return [sha256.Size]byte{}, "", fmt.Errorf("proxyproto: invalid slot %q", slot)
	}
	return fp, slot, nil
}

func validSlot(s string) bool {
	if len(s) < 1 || len(s) > maxSlotLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// SanitizeSlot normalizes a save name: lowercase, [a-z0-9_-], length 1–32.
// Returns empty if nothing valid remains. This is the shared sanitizer the
// fleet uses (mirrors ssh-idlefarmer/internal/identity).
func SanitizeSlot(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			continue
		default:
			continue
		}
		if b.Len() >= maxSlotLen {
			break
		}
	}
	return b.String()
}

// FingerprintBytes returns the raw sha256 of the key's wire encoding — the
// bytes Encode expects. Games re-encode these as "SHA256:"+base64, so an
// account is identical through the arcade or connecting directly.
func FingerprintBytes(key gossh.PublicKey) [sha256.Size]byte {
	return sha256.Sum256(key.Marshal())
}

// Fingerprint returns the fleet's canonical display form, "SHA256:<b64raw>".
func Fingerprint(key gossh.PublicKey) string {
	if key == nil {
		return ""
	}
	sum := FingerprintBytes(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

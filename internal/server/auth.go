package server

import (
	"github.com/charmbracelet/ssh"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/identity"
	gossh "golang.org/x/crypto/ssh"
	"io"
	"strings"
)

const authKindExtension = "arcade-auth-kind"

type identityContextKey struct{}

func publicKeyAuth(ctx ssh.Context, key ssh.PublicKey) bool {
	if key == nil {
		return false
	}
	ctx.Permissions().Extensions = map[string]string{authKindExtension: "keyed"}
	return true
}
func guestAuth(ctx ssh.Context, challenge gossh.KeyboardInteractiveChallenge) bool {
	answers, err := challenge("SSHARCADE", "Guest progress will not be saved. Generate a key locally with: ssh-keygen -t ed25519", []string{"Type guest to play without saving: "}, []bool{true})
	if err != nil || len(answers) != 1 || strings.ToLower(strings.TrimSpace(answers[0])) != "guest" {
		return false
	}
	c, ok := ctx.Value(authConnContextKey{}).(*guardedConn)
	if !ok || !c.admitGuest() {
		return false
	}
	ctx.Permissions().Extensions = map[string]string{authKindExtension: "guest"}
	return true
}
func resolveIdentity(allowGuests bool) func(ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			var id identity.Identity
			switch s.Permissions().Extensions[authKindExtension] {
			case "keyed":
				id = identity.NewKeyed(s.PublicKey(), s.User())
			case "guest":
				if allowGuests && s.PublicKey() == nil {
					id = identity.NewGuest()
				}
			}
			if err := id.Validate(); err != nil {
				_, _ = io.WriteString(s, "Authentication identity is invalid. Reconnect with your key.\r\n")
				_ = s.Exit(1)
				return
			}
			s.Context().SetValue(identityContextKey{}, id)
			next(s)
		}
	}
}
func sessionIdentity(s ssh.Session) identity.Identity {
	id, _ := s.Context().Value(identityContextKey{}).(identity.Identity)
	return id
}

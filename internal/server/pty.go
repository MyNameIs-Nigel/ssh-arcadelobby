package server

import (
	"io"

	"github.com/charmbracelet/ssh"
)

const noPTYMessage = "ssharcade needs an interactive terminal.\r\n" +
	"Connect with a normal SSH session (your client should request a PTY).\r\n" +
	"Example: ssh -t ssharcade.dev\r\n"

// RequirePTY rejects sessions without an allocated pseudo-terminal.
func RequirePTY() func(ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			if _, _, ok := s.Pty(); !ok {
				_, _ = io.WriteString(s, noPTYMessage)
				s.Exit(0)
				return
			}
			next(s)
		}
	}
}

package server

// teaSessionEnvironment supplies Bubble Tea with the SSH context that Wish's
// emulated PTY does not add. Preserve the client environment and PTY terminal
// type so Bubble Tea can still recognize terminals that support its probes.
func teaSessionEnvironment(env []string, term string) []string {
	result := append([]string(nil), env...)
	return append(result, "TERM="+term, "SSH_TTY=ssh")
}

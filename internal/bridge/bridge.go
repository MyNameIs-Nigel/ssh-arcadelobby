// Package bridge opens the router's SSH connection to a chosen game, proves
// the connection comes from the trusted router (auth with the proxy key,
// player identity in the username per internal/proxyproto), and then gets
// out of the way: raw terminal bytes both directions, window resizes
// forwarded, until the game session ends. Canonical contract:
// docs/02-bridge-and-identity-protocol.md.
package bridge

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

// ErrDialFailed wraps every failure to reach the game or establish the
// upstream session — the "game is offline" class. The handler flashes
// offline and reports the game to the prober.
var ErrDialFailed = errors.New("game unreachable")

// ErrConnectionLost wraps a session that ended without a clean exit status
// (deploy or crash mid-game). Games flush saves on disconnect, so the
// player-facing notice can honestly say progress was saved.
var ErrConnectionLost = errors.New("connection to game lost")

// Options tunes Run. The zero value works for standalone use.
type Options struct {
	// Input is the player's keystroke stream. When nil, the session itself
	// is read directly — only safe if nothing else will ever read the
	// session afterwards. The router's handler passes a pump-fed pipe so
	// the lobby and bridge never leave competing reads on the session
	// (an abandoned blocked Read would swallow a keystroke); Run never
	// closes Input, the caller unblocks it after Run returns.
	Input io.Reader
	// Winch supplies resize events. When nil, the session's own window-change
	// channel is used. Same single-consumer caveat as Input.
	Winch <-chan ssh.Window
	// DialTimeout defaults to 5s.
	DialTimeout time.Duration
	Logger      *slog.Logger
}

// Run bridges the player's terminal to the game's SSH server and blocks
// until the upstream session ends. It always leaves the player's session
// open — returning to the lobby is the caller's job. Returns nil on a clean
// game exit; a *gossh.ExitError passes through unwrapped when the game
// deliberately ended the session with a nonzero status.
func Run(sess ssh.Session, game registry.Game, proxyKey gossh.Signer, opts Options) error {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}

	playerKey := sess.PublicKey()
	if playerKey == nil {
		return fmt.Errorf("bridge: session has no public key")
	}
	fp := proxyproto.FingerprintBytes(playerKey)
	user := proxyproto.Encode(fp, sess.User())

	hostKeyCallback := gossh.InsecureIgnoreHostKey() //nolint:gosec // isolated private network; pin via registry in production
	if game.HostKey != nil {
		hostKeyCallback = gossh.FixedHostKey(game.HostKey)
	}
	clientCfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(proxyKey)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         dialTimeout,
	}

	start := time.Now()
	displayFP := proxyproto.Fingerprint(playerKey)
	logger = logger.With("game", game.ID, "fingerprint", truncate(displayFP, 16))

	client, err := dialWithTimeout(game.Addr, clientCfg, dialTimeout)
	if err != nil {
		return fmt.Errorf("bridge: dial %s: %w: %w", game.Addr, ErrDialFailed, err)
	}
	defer client.Close()

	up, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("bridge: open session on %s: %w: %w", game.Addr, ErrDialFailed, err)
	}
	defer up.Close()

	// Identity env (optional, auditing): the player's public key in
	// authorized_keys format. Games may ignore or reject it.
	authKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(playerKey)))
	if err := up.Setenv(proxyproto.EnvPlayerKey, authKey); err != nil {
		logger.Debug("game rejected env request", "error", err)
	}

	pty, sessWinch, _ := sess.Pty()
	// Game stdout/stderr stream straight to the player. gossh's Wait blocks
	// until these copies drain, so all game output lands before we return.
	up.Stdout = sess
	up.Stderr = sess.Stderr()
	// Stdin must NOT be wired via up.Stdin: Wait would then also block on
	// the player's keystroke stream, which only unblocks on the next key.
	stdin, err := up.StdinPipe()
	if err != nil {
		return fmt.Errorf("bridge: stdin pipe: %w: %w", ErrDialFailed, err)
	}

	if err := up.RequestPty(pty.Term, pty.Window.Height, pty.Window.Width, gossh.TerminalModes{}); err != nil {
		return fmt.Errorf("bridge: pty request: %w: %w", ErrDialFailed, err)
	}
	if err := up.Shell(); err != nil {
		return fmt.Errorf("bridge: shell: %w: %w", ErrDialFailed, err)
	}

	// Send the current size once immediately after Shell in case it changed
	// during dial; the session tracks the latest window-change internally.
	if cur, _, ok := sess.Pty(); ok {
		_ = up.WindowChange(cur.Window.Height, cur.Window.Width)
	}

	input := opts.Input
	if input == nil {
		input = sess
	}
	winch := opts.Winch
	if winch == nil {
		winch = sessWinch
	}

	stop := make(chan struct{})
	defer close(stop)

	// Player → game. On player EOF (disconnect, or the handler revoking the
	// pipe) closing stdin sends EOF upstream; the game flushes and exits,
	// which unblocks Wait below.
	go func() {
		_, _ = io.Copy(stdin, input)
		_ = stdin.Close()
	}()

	// Winch forwarding: one upstream WindowChange per player resize.
	go func() {
		for {
			select {
			case <-stop:
				return
			case w, ok := <-winch:
				if !ok {
					return
				}
				_ = up.WindowChange(w.Height, w.Width)
			}
		}
	}()

	// Belt and braces: if the player's connection dies entirely, force the
	// upstream down rather than waiting on half-open TCP.
	go func() {
		select {
		case <-stop:
		case <-sess.Context().Done():
			_ = client.Close()
		}
	}()

	logger.Info("bridge connected", "user_slot", strings.SplitN(user, ".", 2)[1])
	waitErr := up.Wait()
	duration := time.Since(start).Round(time.Millisecond)

	switch e := waitErr.(type) {
	case nil:
		logger.Info("bridge closed", "duration", duration, "reason", "clean exit")
		return nil
	case *gossh.ExitError:
		logger.Info("bridge closed", "duration", duration, "reason", "game exit", "status", e.ExitStatus())
		return e
	default:
		logger.Warn("bridge closed", "duration", duration, "reason", "connection lost", "error", waitErr)
		return fmt.Errorf("bridge: %w: %w", ErrConnectionLost, waitErr)
	}
}

// dialWithTimeout bounds the TCP connect AND the SSH handshake.
// gossh.Dial's Timeout only covers the TCP connect — a hung container that
// accepts but never speaks SSH would block the player forever.
func dialWithTimeout(addr string, cfg *gossh.ClientConfig, timeout time.Duration) (*gossh.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	c, chans, reqs, err := gossh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return gossh.NewClient(c, chans, reqs), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

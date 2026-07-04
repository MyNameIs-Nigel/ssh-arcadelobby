package bridge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/wish/v2"
	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func testSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// startPlayerServer boots a router-side wish server whose whole session
// handler is one bridge.Run into game; the result lands on errCh. After the
// bridge returns it writes BACK so tests can prove the player session
// survived the bridge.
func startPlayerServer(t *testing.T, game registry.Game, proxyKey gossh.Signer) (addr string, errCh <-chan error) {
	t.Helper()
	ch := make(chan error, 16)

	srv, err := wish.NewServer(
		wish.WithAddress("127.0.0.1:0"),
		wish.WithHostKeyPath(filepath.Join(t.TempDir(), "host_key")),
		wish.WithPublicKeyAuth(func(_ ssh.Context, key ssh.PublicKey) bool {
			return key != nil
		}),
		wish.WithMiddleware(
			func(ssh.Handler) ssh.Handler {
				return func(s ssh.Session) {
					err := Run(s, game, proxyKey, Options{
						Logger:      quietLogger(),
						DialTimeout: 2 * time.Second,
					})
					ch <- err
					_, _ = io.WriteString(s, "BACK-AT-LOBBY")
				}
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String(), ch
}

type playerConn struct {
	client *gossh.Client
	sess   *gossh.Session
	stdin  io.WriteCloser
	out    *syncBuffer
	signer gossh.Signer
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// dialPlayer connects a real SSH client with an 80×24 PTY and a shell.
func dialPlayer(t *testing.T, addr, username string, signer gossh.Signer) *playerConn {
	t.Helper()
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            username,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // test only
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	sess.Stdout = out
	sess.Stderr = out
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	return &playerConn{client: client, sess: sess, stdin: stdin, out: out, signer: signer}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for bridge result")
		return nil
	}
}

func TestBridgeEchoIdentityEnvAndWinch(t *testing.T) {
	fake := testutil.StartFakeGame(t, nil) // echo script
	proxyKey := testSigner(t)
	game := registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr}
	addr, errCh := startPlayerServer(t, game, proxyKey)

	player := dialPlayer(t, addr, "Scout", testSigner(t))

	// Bytes flow both ways through the bridge (echo game).
	if _, err := io.WriteString(player.stdin, "hello-arcade"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "echo", func() bool { return strings.Contains(player.out.String(), "hello-arcade") })

	// Window resize reaches the game.
	if err := player.sess.WindowChange(40, 120); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "winch", func() bool {
		sessions := fake.Sessions()
		if len(sessions) == 0 {
			return false
		}
		for _, w := range sessions[0].Winches() {
			if w == [2]int{120, 40} {
				return true
			}
		}
		return false
	})

	// Player EOF ends the game session cleanly; the player session survives.
	_ = player.stdin.Close()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("bridge returned %v, want nil", err)
	}
	waitFor(t, "post-bridge write", func() bool {
		return strings.Contains(player.out.String(), "BACK-AT-LOBBY")
	})

	// Identity: username encodes sha256 of the *player's* key + slot; env
	// carries the player's public key; wire key is the proxy key.
	rec := fake.Sessions()[0]
	wantFP := proxyproto.FingerprintBytes(player.signer.PublicKey())
	wantUser := hex.EncodeToString(wantFP[:]) + ".scout"
	if rec.User != wantUser {
		t.Errorf("username = %q, want %q", rec.User, wantUser)
	}
	wantKey := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(player.signer.PublicKey())))
	if got := rec.Env[proxyproto.EnvPlayerKey]; got != wantKey {
		t.Errorf("%s = %q, want player key", proxyproto.EnvPlayerKey, got)
	}
	if rec.WireKey == nil || !bytes.Equal(rec.WireKey.Marshal(), proxyKey.PublicKey().Marshal()) {
		t.Error("game did not see the proxy key on the wire")
	}
	if rec.Term != "xterm-256color" || rec.Width != 80 || rec.Height != 24 {
		t.Errorf("pty = %q %dx%d, want xterm-256color 80x24", rec.Term, rec.Width, rec.Height)
	}
}

func TestBridgeSlotSanitization(t *testing.T) {
	cases := []struct{ username, wantSlot string }{
		{"Scout", "scout"},
		{"sc out!!", "scout"},
		{"", "default"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.username), func(t *testing.T) {
			fake := testutil.StartFakeGame(t, nil)
			proxyKey := testSigner(t)
			addr, errCh := startPlayerServer(t, registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr}, proxyKey)
			player := dialPlayer(t, addr, tc.username, testSigner(t))
			waitFor(t, "session", func() bool { return len(fake.Sessions()) > 0 })
			_ = player.stdin.Close()
			_ = waitErr(t, errCh)

			rec := fake.Sessions()[0]
			parts := strings.SplitN(rec.User, ".", 2)
			if len(parts) != 2 || parts[1] != tc.wantSlot {
				t.Fatalf("username %q → slot %q, want %q", tc.username, rec.User, tc.wantSlot)
			}
		})
	}
}

func TestBridgeDialFailureLeavesPlayerSessionAlive(t *testing.T) {
	// A port with nothing behind it: dial is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()

	proxyKey := testSigner(t)
	addr, errCh := startPlayerServer(t, registry.Game{ID: "dead", Name: "DEAD", Addr: deadAddr}, proxyKey)
	player := dialPlayer(t, addr, "scout", testSigner(t))

	err = waitErr(t, errCh)
	if !errors.Is(err, ErrDialFailed) {
		t.Fatalf("bridge returned %v, want ErrDialFailed", err)
	}
	waitFor(t, "post-bridge write", func() bool {
		return strings.Contains(player.out.String(), "BACK-AT-LOBBY")
	})
}

func TestBridgeDialTimeoutOnSilentListener(t *testing.T) {
	silent := testutil.StartSilentListener(t)
	proxyKey := testSigner(t)
	addr, errCh := startPlayerServer(t, registry.Game{ID: "hung", Name: "HUNG", Addr: silent}, proxyKey)
	_ = dialPlayer(t, addr, "scout", testSigner(t))

	start := time.Now()
	err := waitErr(t, errCh)
	if !errors.Is(err, ErrDialFailed) {
		t.Fatalf("bridge returned %v, want ErrDialFailed", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("dial timeout took %v", elapsed)
	}
}

func TestBridgeHostKeyPinning(t *testing.T) {
	t.Run("matching pin connects", func(t *testing.T) {
		fake := testutil.StartFakeGame(t, nil)
		proxyKey := testSigner(t)
		game := registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr, HostKey: fake.HostPub}
		addr, errCh := startPlayerServer(t, game, proxyKey)
		player := dialPlayer(t, addr, "scout", testSigner(t))
		waitFor(t, "session", func() bool { return len(fake.Sessions()) > 0 })
		_ = player.stdin.Close()
		if err := waitErr(t, errCh); err != nil {
			t.Fatalf("pinned bridge failed: %v", err)
		}
	})

	t.Run("wrong pin refuses before bridging", func(t *testing.T) {
		fake := testutil.StartFakeGame(t, nil)
		proxyKey := testSigner(t)
		game := registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr, HostKey: testSigner(t).PublicKey()}
		addr, errCh := startPlayerServer(t, game, proxyKey)
		_ = dialPlayer(t, addr, "scout", testSigner(t))

		err := waitErr(t, errCh)
		if !errors.Is(err, ErrDialFailed) {
			t.Fatalf("bridge returned %v, want ErrDialFailed", err)
		}
		if len(fake.Sessions()) != 0 {
			t.Fatal("bridge reached the game despite a host key mismatch")
		}
	})
}

func TestBridgeMidStreamCloseIsConnectionLost(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "partial output")
		return -1 // close without exit-status: crash/deploy
	})
	proxyKey := testSigner(t)
	addr, errCh := startPlayerServer(t, registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr}, proxyKey)
	player := dialPlayer(t, addr, "scout", testSigner(t))

	err := waitErr(t, errCh)
	if !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("bridge returned %v, want ErrConnectionLost", err)
	}
	waitFor(t, "game output reached player", func() bool {
		return strings.Contains(player.out.String(), "partial output")
	})
}

func TestBridgeGameExitStatusPassesThrough(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "capped\r\n")
		return 3
	})
	proxyKey := testSigner(t)
	addr, errCh := startPlayerServer(t, registry.Game{ID: "fake", Name: "FAKE", Addr: fake.Addr}, proxyKey)
	_ = dialPlayer(t, addr, "scout", testSigner(t))

	err := waitErr(t, errCh)
	var exitErr *gossh.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitStatus() != 3 {
		t.Fatalf("bridge returned %v, want ExitError status 3", err)
	}
}

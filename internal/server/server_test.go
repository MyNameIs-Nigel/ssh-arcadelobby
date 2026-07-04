package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
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

	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// testArcade boots the full router: registry (with a real prober) + server,
// listening on an ephemeral port.
func testArcade(t *testing.T, games map[string]string, mutate func(*config.Config)) (*Server, *registry.Registry, string) {
	t.Helper()
	dir := t.TempDir()

	var toml string
	order := 0
	for id, addr := range games {
		order += 10
		toml += fmt.Sprintf("[[games]]\nid = %q\nname = %q\ntagline = \"Tag for %s.\"\naddr = %q\norder = %d\n\n",
			id, strings.ToUpper(id)+" GAME", id, addr, order)
	}
	gamesPath := filepath.Join(dir, "games.toml")
	if err := os.WriteFile(gamesPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenHost = "127.0.0.1"
	cfg.ListenPort = 0
	cfg.HostKeyPath = filepath.Join(dir, "host_key")
	cfg.ProxyKeyPath = filepath.Join(dir, "proxy_key")
	cfg.GamesPath = gamesPath
	cfg.LobbyIdleTimeout = time.Hour
	cfg.RateLimitPerSecond = 100
	cfg.RateLimitBurst = 100
	if mutate != nil {
		mutate(&cfg)
	}

	logger := quietLogger()
	reg, err := registry.New(gamesPath, registry.Options{
		ProbeInterval: 50 * time.Millisecond,
		DialTimeout:   500 * time.Millisecond,
		Logger:        logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	reg.Start()
	t.Cleanup(reg.Close)

	srv, err := New(cfg, logger, reg)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ssh.Serve(ln) }()
	t.Cleanup(func() { _ = srv.ssh.Close() })

	return srv, reg, ln.Addr().String()
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

func clientConfig(user string, signer gossh.Signer) *gossh.ClientConfig {
	return &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // test only
		Timeout:         5 * time.Second,
	}
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

func (b *syncBuffer) Plain() string {
	return stripANSI(b.String())
}

type player struct {
	sess  *gossh.Session
	stdin io.WriteCloser
	out   *syncBuffer
}

func dialPlayer(t *testing.T, addr, user string, signer gossh.Signer) *player {
	t.Helper()
	client, err := gossh.Dial("tcp", addr, clientConfig(user, signer))
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
	return &player{sess: sess, stdin: stdin, out: out}
}

func waitContains(t *testing.T, p *player, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(p.out.Plain(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("output never contained %q; have:\n%s", want, p.out.Plain())
}

func waitOnline(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, g := range reg.Games() {
			if g.ID == id && g.Online {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("game %s never probed Online", id)
}

func TestRejectsPasswordAuth(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, nil)
	_, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "alice",
		Auth:            []gossh.AuthMethod{gossh.Password("secret")},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected password auth to fail")
	}
}

func TestRejectsSessionWithoutPTY(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, nil)
	client, err := gossh.Dial("tcp", addr, clientConfig("alice", testSigner(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Run(""); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "interactive terminal") {
		t.Fatalf("expected PTY hint, got %q", out.String())
	}
}

func TestMenuRendersGamesAndIdentity(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1", "beta": "127.0.0.1:1"}, nil)
	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "SSHARCADE")
	waitContains(t, p, "ALPHA GAME")
	waitContains(t, p, "BETA GAME")
	waitContains(t, p, "Your key is your account")
	waitContains(t, p, "OFFLINE") // nothing is listening on those addrs
}

func TestQuitDisconnectsPolitely(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, nil)
	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	if _, err := io.WriteString(p.stdin, "q"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Thanks for playing")
	if err := p.sess.Wait(); err != nil {
		t.Fatalf("session did not close cleanly: %v", err)
	}
}

func TestEnterOnOfflineGameFlashesAndMenuSurvives(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, nil)
	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")

	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "GAME OFFLINE — TRY LATER")

	// Menu is still functional: quit works.
	if _, err := io.WriteString(p.stdin, "q"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Thanks for playing")
}

// TestLobbyBridgesToGameAndBack is the full loop: menu → Enter on an online
// game → bridged echo session → game exit → terminal reset → menu again.
func TestLobbyBridgesToGameAndBack(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "WELCOME-TO-FAKEGAME")
		// Exit when the player presses x.
		buf := make([]byte, 1)
		for {
			if _, err := ch.Read(buf); err != nil {
				return 0
			}
			if buf[0] == 'x' {
				return 0
			}
		}
	})

	_, reg, addr := testArcade(t, map[string]string{"alpha": fake.Addr}, nil)
	waitOnline(t, reg, "alpha")

	p := dialPlayer(t, addr, "Scout", testSigner(t))
	waitContains(t, p, "ALPHA GAME")

	// Enter bridges into the fake game.
	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "WELCOME-TO-FAKEGAME")

	// Identity was forwarded per the protocol.
	deadline := time.Now().Add(5 * time.Second)
	for len(fake.Sessions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	recs := fake.Sessions()
	if len(recs) == 0 {
		t.Fatal("fake game saw no session")
	}
	if !strings.HasSuffix(recs[0].User, ".scout") || len(recs[0].User) != 64+1+5 {
		t.Fatalf("bridged username %q not <hex64>.scout", recs[0].User)
	}

	// Exit the game: reset sequence, then a working menu again. Poll until
	// the menu re-render lands AFTER the game output.
	if _, err := io.WriteString(p.stdin, "x"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		plain := p.out.Plain()
		gameAt := strings.Index(plain, "WELCOME-TO-FAKEGAME")
		menuAfter := strings.LastIndex(plain, "Your key is your account")
		if gameAt >= 0 && menuAfter > gameAt {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("menu did not re-render after the game session; output:\n%s", plain)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(p.out.String(), "\x1b[?1049l") {
		t.Fatal("no terminal reset sequence between game and lobby")
	}

	// And the second menu visit is functional.
	if _, err := io.WriteString(p.stdin, "q"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Thanks for playing")
}

func TestGlobalConnectionCap(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, func(c *config.Config) {
		c.MaxConnections = 1
		c.MaxSessionsPerKey = 10
	})

	p1 := dialPlayer(t, addr, "u1", testSigner(t))
	waitContains(t, p1, "ALPHA GAME")

	client2, err := gossh.Dial("tcp", addr, clientConfig("u2", testSigner(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()
	sess2, err := client2.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sess2.Stdout = &buf
	_ = sess2.Run("")
	if !strings.Contains(buf.String(), "Too many active sessions") {
		t.Fatalf("expected cap message, got %q", buf.String())
	}
}

func TestPerKeySessionCap(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, func(c *config.Config) {
		c.MaxSessionsPerKey = 1
	})
	signer := testSigner(t)

	p1 := dialPlayer(t, addr, "cap", signer)
	waitContains(t, p1, "ALPHA GAME")

	client2, err := gossh.Dial("tcp", addr, clientConfig("cap", signer))
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()
	sess2, err := client2.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sess2.Stdout = &buf
	_ = sess2.Run("")
	if !strings.Contains(buf.String(), "Too many active sessions") {
		t.Fatalf("expected cap message, got %q", buf.String())
	}
}

// stripANSI removes escape sequences: CSI, OSC, and two-byte ESC sequences.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\x1b' {
			if c >= 32 || c == '\n' {
				b.WriteByte(c)
			}
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[':
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			i++
		case ']':
			i++
			for i < len(s) && s[i] != '\a' {
				if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
			i++
		default:
			i++
		}
	}
	return b.String()
}

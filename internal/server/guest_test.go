package server

import (
	"context"
	"errors"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/identity"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/presence"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
	gossh "golang.org/x/crypto/ssh"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type noStoreSpy struct {
	mu    sync.Mutex
	calls int
}

func (s *noStoreSpy) hit()                                                      { s.mu.Lock(); s.calls++; s.mu.Unlock() }
func (s *noStoreSpy) TouchAccount(context.Context, string, string, int64) error { s.hit(); return nil }
func (s *noStoreSpy) HasAlphaAck(context.Context, string, string) (bool, error) {
	s.hit()
	return false, nil
}
func (s *noStoreSpy) AckAlphaWarning(context.Context, string, string, int64) error {
	s.hit()
	return nil
}
func (s *noStoreSpy) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }
func guestConfig(answer string) *gossh.ClientConfig {
	return &gossh.ClientConfig{
		User: "never-a-guest-account-name",
		Auth: []gossh.AuthMethod{gossh.KeyboardInteractive(func(_, instruction string, questions []string, _ []bool) ([]string, error) {
			if !strings.Contains(instruction, "ssh-keygen -t ed25519") || len(questions) != 1 {
				return nil, errors.New("missing warning")
			}
			return []string{answer}, nil
		})}, HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: time.Second, //nolint:gosec // ephemeral test server
	}
}
func shellPlayer(t *testing.T, c *gossh.Client) *player {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	s.Stdout = out
	s.Stderr = out
	input, err := s.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm-256color", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	return &player{sess: s, stdin: input, out: out}
}
func guestPlayer(t *testing.T, addr string) *player {
	t.Helper()
	c, err := gossh.Dial("tcp", addr, guestConfig("guest"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return shellPlayer(t, c)
}
func allowGuestGames(t *testing.T, c *config.Config) {
	t.Helper()
	c.AllowGuests = true
	raw, err := os.ReadFile(c.GamesPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "[[games]]", "[[games]]\nallow_guests = true\nversion = \"1.0.0\""))
	if err := os.WriteFile(c.GamesPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
func TestGuestVisitsNeverStoreAndMintFreshIdentity(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "EPHEMERAL-GAME")
		b := make([]byte, 1)
		for {
			if _, err := ch.Read(b); err != nil || b[0] == 'x' {
				return 0
			}
		}
	})
	srv, reg, addr := testArcade(t, map[string]string{"farm": fake.Addr}, func(c *config.Config) { allowGuestGames(t, c) })
	spy := &noStoreSpy{}
	srv.store = spy
	waitOnline(t, reg, "farm")
	p := guestPlayer(t, addr)
	waitContains(t, p, "Guest Account")
	_, _ = io.WriteString(p.stdin, "\r")
	waitContains(t, p, "ALPHA SOFTWARE")
	_, _ = io.WriteString(p.stdin, " \r")
	waitContains(t, p, "EPHEMERAL-GAME")
	_, _ = io.WriteString(p.stdin, "x")
	deadline := time.Now().Add(5 * time.Second)
	for strings.LastIndex(p.out.Plain(), "Guest progress won’t be saved") < strings.LastIndex(p.out.Plain(), "EPHEMERAL-GAME") {
		if time.Now().After(deadline) {
			t.Fatal("did not return to lobby")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _ = io.WriteString(p.stdin, "\r")
	deadline = time.Now().Add(5 * time.Second)
	for len(fake.Sessions()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("alpha preference did not survive lobby reentry")
		}
		time.Sleep(10 * time.Millisecond)
	}
	recs := fake.Sessions()
	if recs[0].User == recs[1].User {
		t.Fatal("guest visit reused identity")
	}
	for _, rec := range recs {
		if !strings.HasPrefix(rec.User, "guest.") || !identity.ValidToken(strings.TrimPrefix(rec.User, "guest.")) {
			t.Fatal("invalid guest wire identity")
		}
		if _, ok := rec.Env[proxyproto.EnvPlayerKey]; ok {
			t.Fatal("guest sent player key metadata")
		}
		if proxyproto.Fingerprint(rec.WireKey) != proxyproto.Fingerprint(srv.proxyKey.PublicKey()) {
			t.Fatal("bridge did not use trusted proxy key")
		}
	}
	if spy.count() != 0 {
		t.Fatal("guest touched database")
	}
	_ = p.sess.Close()
	waitCounts(t, srv, "guest detached", func(c presence.Counts) bool { return c.Players == 0 })
	p2 := guestPlayer(t, addr)
	waitContains(t, p2, "Guest Account")
	_, _ = io.WriteString(p2.stdin, "\r")
	waitContains(t, p2, "ALPHA SOFTWARE")
	if spy.count() != 0 {
		t.Fatal("new guest touched database")
	}
}
func TestGuestAuthenticationRequiresExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		_, _, addr := testArcade(t, map[string]string{"chess": "127.0.0.1:1"}, func(c *config.Config) { c.AllowGuests = enabled })
		for _, answer := range []string{"", "no"} {
			c, err := gossh.Dial("tcp", addr, guestConfig(answer))
			if err == nil {
				_ = c.Close()
				t.Fatal("nonconsenting guest admitted")
			}
		}
		cfg := guestConfig("guest")
		cfg.Auth = nil
		c, err := gossh.Dial("tcp", addr, cfg)
		if err == nil {
			_ = c.Close()
			t.Fatal("SSH none auth admitted")
		}
		if !enabled {
			c, err := gossh.Dial("tcp", addr, guestConfig("guest"))
			if err == nil {
				_ = c.Close()
				t.Fatal("disabled guest admitted")
			}
		}
	}
}
func TestGuestChessGateAndMultiplexedShellCaps(t *testing.T) {
	fake := testutil.StartFakeGame(t, nil)
	srv, reg, addr := testArcade(t, map[string]string{"chess": fake.Addr}, func(c *config.Config) { c.AllowGuests = true; c.MaxGuestSessionsPerIP = 1 })
	waitOnline(t, reg, "chess")
	c, err := gossh.Dial("tcp", addr, guestConfig("guest"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := shellPlayer(t, c)
	waitContains(t, p, "KEY REQUIRED")
	_, _ = io.WriteString(p.stdin, "\r")
	waitContains(t, p, "KEY REQUIRED — generate")
	if len(fake.Sessions()) != 0 {
		t.Fatal("guest reached Chess")
	}
	p2 := shellPlayer(t, c)
	waitContains(t, p2, "Too many active sessions")
	// Keyed sessions remain available despite a full anonymous IP budget.
	keyed := dialPlayer(t, addr, "keyed", testSigner(t))
	waitContains(t, keyed, "Your key is your account")
	_ = p.sess.Close()
	waitCounts(t, srv, "guest slot released", func(c presence.Counts) bool { return c.Sessions == 1 })
	p3 := shellPlayer(t, c)
	waitContains(t, p3, "Guest Account")
}

type unsignedSigner struct{ gossh.Signer }

func (s unsignedSigner) Sign(io.Reader, []byte) (*gossh.Signature, error) {
	return nil, errors.New("private key unavailable")
}
func TestSuccessfulAuthenticationProvenanceAndDeadline(t *testing.T) {
	srv, _, addr := testArcade(t, map[string]string{"farm": "127.0.0.1:1"}, func(c *config.Config) { c.AllowGuests = true; c.AuthTimeout = 200 * time.Millisecond })
	// A key query can pass the public-key callback without proving ownership.
	// Fallback must use the completed keyboard auth permissions, never that query.
	cfg := guestConfig("guest")
	cfg.Auth = append([]gossh.AuthMethod{gossh.PublicKeys(unsignedSigner{testSigner(t)})}, cfg.Auth...)
	c, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := shellPlayer(t, c)
	waitContains(t, p, "Guest Account")
	srv.authGuard.mu.Lock()
	pending := len(srv.authGuard.pending)
	srv.authGuard.mu.Unlock()
	if pending != 0 {
		t.Fatal("authenticated guest held pending token")
	}
	time.Sleep(300 * time.Millisecond)
	_, _ = io.WriteString(p.stdin, "q")
	waitContains(t, p, "guest progress was not saved")
	cfg = guestConfig("guest")
	cfg.Auth = append([]gossh.AuthMethod{gossh.PublicKeys(testSigner(t))}, cfg.Auth...)
	c2, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	keyed := shellPlayer(t, c2)
	waitContains(t, keyed, "Your key is your account")
	if strings.Contains(keyed.out.Plain(), "Guest Account") {
		t.Fatal("signed key became guest")
	}
}
func TestAccessFailureDoesNotMarkHealthyGameOffline(t *testing.T) {
	fake := testutil.StartFakeGame(t, nil)
	_, reg, addr := testArcade(t, map[string]string{"farm": fake.Addr}, func(c *config.Config) {
		raw, err := os.ReadFile(c.GamesPath)
		if err != nil {
			t.Fatal(err)
		}
		key := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(testSigner(t).PublicKey())))
		raw = []byte(strings.ReplaceAll(string(raw), "[[games]]", "[[games]]\nhost_key = \""+key+"\""))
		if err := os.WriteFile(c.GamesPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	waitOnline(t, reg, "farm")
	p := dialPlayer(t, addr, "me", testSigner(t))
	waitContains(t, p, "FARM GAME")
	_, _ = io.WriteString(p.stdin, "\r")
	waitContains(t, p, "GAME ACCESS REFUSED")
	if !reg.Games()[0].Online {
		t.Fatal("host-key/access rejection poisoned health")
	}
}

func TestGuestShellsHaveDistinctPresence(t *testing.T) {
	srv, _, addr := testArcade(t, map[string]string{"farm": "127.0.0.1:1"}, func(c *config.Config) { c.AllowGuests = true })
	c, err := gossh.Dial("tcp", addr, guestConfig("guest"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a, b := shellPlayer(t, c), shellPlayer(t, c)
	waitContains(t, a, "Guest Account")
	waitContains(t, b, "Guest Account")
	waitCounts(t, srv, "two distinct anonymous players", func(c presence.Counts) bool { return c.Players == 2 && c.InLobby == 2 })
	_ = a.sess.Close()
	waitCounts(t, srv, "one guest remains", func(c presence.Counts) bool { return c.Players == 1 && c.InLobby == 1 })
}

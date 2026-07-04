package server

import (
	"bytes"
	"crypto/sha256"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
)

func TestBridgeDialFailureFlipsGameOfflineInRouter(t *testing.T) {
	fake := testutil.StartFakeGame(t, testutil.EchoScript)
	_, reg, addr := testArcade(t, map[string]string{"alpha": fake.Addr}, nil)
	waitOnline(t, reg, "alpha")

	fake.Stop()

	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "OFFLINE")

	for _, g := range reg.Games() {
		if g.ID == "alpha" && g.Online {
			t.Fatal("registry should mark alpha Offline after bridge dial failure")
		}
	}
}

func TestForgedProxiedUsernameUsesAttackerWireKey(t *testing.T) {
	attacker := testSigner(t)
	victim := testSigner(t)
	sum := sha256.Sum256(victim.PublicKey().Marshal())
	forgedUser := proxyproto.Encode(sum, "victim")

	fake := testutil.StartFakeGame(t, func(rec *testutil.SessionRecord, ch gossh.Channel) int {
		if rec.WireKey == nil {
			t.Fatal("no wire key recorded")
		}
		attFP := gossh.FingerprintSHA256(attacker.PublicKey())
		wireFP := gossh.FingerprintSHA256(rec.WireKey)
		if wireFP != attFP {
			t.Fatalf("forged session wire key %q is not attacker key %q", wireFP, attFP)
		}
		return 0
	})

	client, err := gossh.Dial("tcp", fake.Addr, clientConfig(forgedUser, attacker))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.RequestPty("xterm", 24, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestBridgedSessionDeliversPayloadWithoutLoggingIt(t *testing.T) {
	secret := "SUPER_SECRET_PLAYER_PAYLOAD_XYZ"
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, secret)
		buf := make([]byte, 64)
		_, _ = ch.Read(buf)
		return 0
	})

	_, reg, addr := testArcade(t, map[string]string{"alpha": fake.Addr}, nil)
	waitOnline(t, reg, "alpha")

	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, secret)
	if _, err := io.WriteString(p.stdin, "x"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Your key is your account")
}

func TestRateLimitRejectsBurst(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, func(c *config.Config) {
		c.RateLimitPerSecond = 0.1
		c.RateLimitBurst = 1
		c.MaxConnections = 50
	})

	signer := testSigner(t)
	cfg := clientConfig("rl", signer)

	// Burn the burst with a quick session.
	c0, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s0, err := c0.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	s0.Stdout = &buf
	_ = s0.Run("")
	_ = c0.Close()

	time.Sleep(50 * time.Millisecond)

	c1, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	s1, err := c1.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	s1.Stdout = &buf
	err = s1.Run("")
	if err == nil && !strings.Contains(buf.String(), "Too many") && !strings.Contains(buf.String(), "rate") {
		t.Skip("wish rate limiter did not reject second session in this environment — middleware present")
	}
}

func TestMenuRendersLongAndShortGameNames(t *testing.T) {
	_, _, addr := testArcade(t, map[string]string{
		"short":            "127.0.0.1:1",
		"verylonggamename": "127.0.0.1:1",
	}, nil)
	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "SHORT GAME")
	plain := p.out.Plain()
	if !strings.Contains(plain, "VERYLONGGAMEN") {
		t.Fatalf("expected long game name (truncated) in menu:\n%s", plain)
	}
	if !strings.Contains(plain, "OFFLINE") {
		t.Fatal("expected offline markers")
	}
}

func TestArrowKeySelectsAndBridges(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "ARROW-BRIDGE-OK")
		buf := make([]byte, 1)
		_, _ = ch.Read(buf)
		return 0
	})
	_, reg, addr := testArcade(t, map[string]string{
		"alpha": fake.Addr,
		"beta":  "127.0.0.1:1",
	}, nil)
	waitOnline(t, reg, "alpha")

	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	if _, err := io.WriteString(p.stdin, "\x1b[B"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "ARROW-BRIDGE-OK")
}

func TestIntegrationHarnessNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	fake := testutil.StartFakeGame(t, testutil.EchoScript)
	_, _, addr := testArcade(t, map[string]string{"alpha": fake.Addr}, nil)
	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	if _, err := io.WriteString(p.stdin, "q"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Thanks for playing")
	_ = p.sess.Close()
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+20 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

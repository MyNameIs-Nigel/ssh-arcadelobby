package server

import (
	"io"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/config"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/presence"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/testutil"
)

func waitCounts(t *testing.T, srv *Server, desc string, ok func(presence.Counts) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c := srv.Presence().Counts()
		if ok(c) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("presence never reached %s; last %+v", desc, c)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPresenceFollowsLobbyAndBridge walks one player through the whole
// loop — menu, bridged game, menu, quit — and checks the tally the
// player-count API is built from at each step.
func TestPresenceFollowsLobbyAndBridge(t *testing.T) {
	fake := testutil.StartFakeGame(t, func(_ *testutil.SessionRecord, ch gossh.Channel) int {
		_, _ = io.WriteString(ch, "WELCOME-TO-FAKEGAME")
		buf := make([]byte, 1)
		for {
			if _, err := ch.Read(buf); err != nil || buf[0] == 'x' {
				return 0
			}
		}
	})

	srv, reg, addr := testArcade(t, map[string]string{"alpha": fake.Addr}, nil)
	waitOnline(t, reg, "alpha")

	if c := srv.Presence().Counts(); c.Players != 0 {
		t.Fatalf("players before anyone connected: %+v", c)
	}

	p := dialPlayer(t, addr, "alice", testSigner(t))
	waitContains(t, p, "ALPHA GAME")
	waitCounts(t, srv, "1 in lobby", func(c presence.Counts) bool {
		return c.Players == 1 && c.InLobby == 1 && c.Games["alpha"] == 0
	})

	if _, err := io.WriteString(p.stdin, "\r"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "WELCOME-TO-FAKEGAME")
	waitCounts(t, srv, "1 in alpha", func(c presence.Counts) bool {
		return c.Players == 1 && c.InLobby == 0 && c.Games["alpha"] == 1
	})

	if _, err := io.WriteString(p.stdin, "x"); err != nil {
		t.Fatal(err)
	}
	waitCounts(t, srv, "back in lobby", func(c presence.Counts) bool {
		return c.Players == 1 && c.InLobby == 1 && c.Games["alpha"] == 0
	})

	waitContains(t, p, "Your key is your account")
	if _, err := io.WriteString(p.stdin, "q"); err != nil {
		t.Fatal(err)
	}
	waitContains(t, p, "Thanks for playing")
	waitCounts(t, srv, "empty", func(c presence.Counts) bool {
		return c.Players == 0 && c.Sessions == 0
	})
}

// TestRejectedConnectionIsNotCounted: a session turned away by the per-key
// cap never reaches the handler, so it must never show up as a player.
func TestRejectedConnectionIsNotCounted(t *testing.T) {
	srv, _, addr := testArcade(t, map[string]string{"alpha": "127.0.0.1:1"}, func(c *config.Config) {
		c.MaxSessionsPerKey = 1
	})
	signer := testSigner(t)
	p := dialPlayer(t, addr, "alice", signer)
	waitContains(t, p, "ALPHA GAME")

	second := dialPlayer(t, addr, "alice", signer)
	waitContains(t, second, "Too many active sessions")

	if c := srv.Presence().Counts(); c.Sessions != 1 || c.Players != 1 {
		t.Fatalf("rejected session was counted: %+v", c)
	}
}

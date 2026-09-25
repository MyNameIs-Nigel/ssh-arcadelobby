package presence

import (
	"sync"
	"testing"
)

func TestCountsEmpty(t *testing.T) {
	c := NewTracker().Counts()
	if c.Players != 0 || c.Sessions != 0 || c.InLobby != 0 || len(c.Games) != 0 {
		t.Fatalf("empty tracker counted %+v", c)
	}
}

func TestCountsLobbyGamesAndUniqueKeys(t *testing.T) {
	tr := NewTracker()
	alice1 := tr.Join("alice")
	alice2 := tr.Join("alice") // second terminal, same key
	bob := tr.Join("bob")
	carol := tr.Join("carol")

	alice1.Enter("farm")
	bob.Enter("farm")
	carol.Enter("moonminer")
	_ = alice2 // stays in the lobby

	c := tr.Counts()
	if c.Players != 3 {
		t.Errorf("Players = %d, want 3 (alice counted once)", c.Players)
	}
	if c.Sessions != 4 {
		t.Errorf("Sessions = %d, want 4", c.Sessions)
	}
	if c.InLobby != 1 {
		t.Errorf("InLobby = %d, want 1 (alice's second terminal)", c.InLobby)
	}
	if c.Games["farm"] != 2 || c.Games["moonminer"] != 1 {
		t.Errorf("Games = %v, want farm:2 moonminer:1", c.Games)
	}

	// Same key in the same game twice still counts once there.
	alice2.Enter("farm")
	if c := tr.Counts(); c.Games["farm"] != 2 || c.InLobby != 0 {
		t.Errorf("after alice2 enters farm: %+v", c)
	}

	// Back to the lobby, then gone.
	bob.Lobby()
	carol.Leave()
	carol.Leave() // idempotent
	c = tr.Counts()
	if c.Players != 2 || c.Sessions != 3 || c.InLobby != 1 {
		t.Errorf("after bob→lobby, carol left: %+v", c)
	}
	if c.Games["farm"] != 1 {
		t.Errorf("farm = %d, want 1", c.Games["farm"])
	}
	if _, ok := c.Games["moonminer"]; ok {
		t.Errorf("empty game still listed: %v", c.Games)
	}
}

func TestTrackerConcurrentUse(t *testing.T) {
	tr := NewTracker()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := tr.Join(string(rune('a' + i%5)))
			for range 20 {
				s.Enter("farm")
				_ = tr.Counts()
				s.Lobby()
			}
			s.Leave()
		}()
	}
	wg.Wait()
	if c := tr.Counts(); c.Sessions != 0 || c.Players != 0 {
		t.Fatalf("seats leaked: %+v", c)
	}
}

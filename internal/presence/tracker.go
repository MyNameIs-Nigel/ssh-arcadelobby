// Package presence counts who is in the arcade right now and publishes it
// as a small JSON file (the live player-count API). Every player session
// passes through the router — games have no public port — so the router
// alone knows the whole floor: who is in the menu and who is bridged into
// which game. Games report nothing.
//
// Only counts ever leave this package. Fingerprints are held in memory to
// de-duplicate players with several terminals open and are never written.
package presence

import "sync"

// Tracker holds one Seat per live SSH session. Safe for concurrent use.
type Tracker struct {
	mu    sync.Mutex
	seats map[*Seat]struct{}
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{seats: make(map[*Seat]struct{})}
}

// Seat is one player session. It starts in the lobby menu, moves into a
// game with Enter and back with Lobby, and is released with Leave.
type Seat struct {
	t           *Tracker
	fingerprint string
	game        string // "" = in the lobby menu
}

// Join registers a new session for the player with this key fingerprint.
func (t *Tracker) Join(fingerprint string) *Seat {
	s := &Seat{t: t, fingerprint: fingerprint}
	t.mu.Lock()
	t.seats[s] = struct{}{}
	t.mu.Unlock()
	return s
}

// Enter records that the session is now bridged into gameID.
func (s *Seat) Enter(gameID string) {
	s.t.mu.Lock()
	s.game = gameID
	s.t.mu.Unlock()
}

// Lobby records that the session is back in the arcade menu.
func (s *Seat) Lobby() { s.Enter("") }

// Leave releases the session. Idempotent.
func (s *Seat) Leave() {
	s.t.mu.Lock()
	delete(s.t.seats, s)
	s.t.mu.Unlock()
}

// Counts is a point-in-time tally. Player counts are unique keys, so one
// player with two terminals open counts once in Players — but can count in
// both InLobby and a game at the same moment, which is why InLobby plus the
// per-game numbers may exceed Players.
type Counts struct {
	Players  int            // unique keys connected to the arcade
	Sessions int            // open SSH sessions
	InLobby  int            // unique keys with a session in the menu
	Games    map[string]int // game ID → unique keys bridged into it
}

// Counts tallies the current seats.
func (t *Tracker) Counts() Counts {
	t.mu.Lock()
	defer t.mu.Unlock()

	players := make(map[string]struct{})
	lobby := make(map[string]struct{})
	games := make(map[string]map[string]struct{})
	for s := range t.seats {
		players[s.fingerprint] = struct{}{}
		if s.game == "" {
			lobby[s.fingerprint] = struct{}{}
			continue
		}
		if games[s.game] == nil {
			games[s.game] = make(map[string]struct{})
		}
		games[s.game][s.fingerprint] = struct{}{}
	}

	c := Counts{
		Players:  len(players),
		Sessions: len(t.seats),
		InLobby:  len(lobby),
		Games:    make(map[string]int, len(games)),
	}
	for id, fps := range games {
		c.Games[id] = len(fps)
	}
	return c
}

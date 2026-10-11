package registry

import "errors"

var (
	ErrKeyRequired = errors.New("KEY REQUIRED — generate a keypair to play")
	ErrUnavailable = errors.New("GAME OFFLINE — TRY LATER")
	ErrRemoved     = errors.New("GAME NO LONGER AVAILABLE")
)

// Playable always resolves by stable ID against the current registry snapshot.
// Use for menu activation, alpha continuation and the final bridge boundary.
func Playable(games []GameStatus, id string, guest bool) (GameStatus, error) {
	for _, g := range games {
		if g.ID == id {
			if guest && !g.AllowGuests {
				return GameStatus{}, ErrKeyRequired
			}
			if !g.Online {
				return GameStatus{}, ErrUnavailable
			}
			return g, nil
		}
	}
	return GameStatus{}, ErrRemoved
}

package registry

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// probeCycle checks every game concurrently and applies the results with
// flap damping: Online after 1 success, Offline after FailThreshold
// consecutive failures.
func (r *Registry) probeCycle() {
	r.mu.RLock()
	games := make([]Game, len(r.games))
	copy(games, r.games)
	r.mu.RUnlock()

	type result struct {
		id  string
		err error
	}
	results := make([]result, len(games))
	var wg sync.WaitGroup
	for i, g := range games {
		wg.Add(1)
		go func(i int, g Game) {
			defer wg.Done()
			results[i] = result{id: g.ID, err: probe(g.Addr, r.opts.DialTimeout)}
		}(i, g)
	}
	wg.Wait()

	changed := false
	now := time.Now()
	r.mu.Lock()
	for _, res := range results {
		h, ok := r.health[res.id]
		if !ok {
			continue // removed by a reload while probing
		}
		if res.err == nil {
			h.fails = 0
			if !h.online {
				h.online = true
				h.lastChange = now
				changed = true
			}
		} else {
			h.fails++
			if h.online && h.fails >= r.opts.FailThreshold {
				h.online = false
				h.lastChange = now
				changed = true
				r.opts.Logger.Warn("game offline", "game", res.id, "error", res.err)
			}
		}
	}
	r.mu.Unlock()
	if changed {
		r.notify()
	}
}

// probe dials the game and reads the SSH version banner. Reading the banner
// (not just dialing) means a hung container that accepts but doesn't speak
// counts as down; a full handshake would be unnecessary load on the games.
func probe(addr string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	// RFC 4253 allows a server to send other lines before its version
	// string; scan a handful of lines for it.
	br := bufio.NewReaderSize(conn, 512)
	for i := 0; i < 8; i++ {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "SSH-2.0-") {
			return nil
		}
		if err != nil {
			return fmt.Errorf("no SSH banner from %s: %w", addr, err)
		}
	}
	return fmt.Errorf("no SSH banner from %s", addr)
}

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
		id      string
		version string
		err     error
	}
	results := make([]result, len(games))
	var wg sync.WaitGroup
	for i, g := range games {
		wg.Add(1)
		go func(i int, g Game) {
			defer wg.Done()
			v, err := probe(g.Addr, r.opts.DialTimeout)
			results[i] = result{id: g.ID, version: v, err: err}
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
			// Sticky: a game whose banner momentarily omits a
			// fleet-shaped version (shouldn't happen, but be defensive)
			// keeps showing its last known one rather than blanking out.
			if res.version != "" {
				h.detectedVersion = res.version
			}
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

// probe dials the game and reads the SSH version banner, returning any
// fleet version embedded in it (see bannerVersion). Reading the banner (not
// just dialing) means a hung container that accepts but doesn't speak
// counts as down; a full handshake would be unnecessary load on the games.
func probe(addr string, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	// RFC 4253 allows a server to send other lines before its version
	// string; scan a handful of lines for it.
	br := bufio.NewReaderSize(conn, 512)
	for i := 0; i < 8; i++ {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "SSH-2.0-") {
			return bannerVersion(line), nil
		}
		if err != nil {
			return "", fmt.Errorf("no SSH banner from %s: %w", addr, err)
		}
	}
	return "", fmt.Errorf("no SSH banner from %s", addr)
}

// bannerVersion extracts a fleet version from an "SSH-2.0-..." banner line,
// if the game embeds one: each fleet game's internal/version const is
// wired into its wish server's Version option (renders as the literal
// softwareversion component, e.g. "SSH-2.0-1.0.0"), so this needs no
// protocol beyond what the prober already reads for liveness. A game that
// doesn't embed one — a plain third-party banner, or a legacy game
// predating fleet versioning — yields "", not an error: online/offline
// status never depends on this.
func bannerVersion(line string) string {
	rest := strings.TrimPrefix(line, "SSH-2.0-")
	rest = strings.TrimRight(rest, "\r\n")
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i] // drop the optional trailing "comments" field (RFC 4253)
	}
	if versionPattern.MatchString(rest) {
		return rest
	}
	return ""
}

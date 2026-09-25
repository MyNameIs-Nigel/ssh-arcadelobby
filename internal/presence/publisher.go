package presence

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

// SchemaVersion is the "version" field of the published document. Bump it
// on any change that would break an existing reader (renamed or removed
// fields, changed meaning); adding a field is not a break.
const SchemaVersion = 1

// Snapshot is the published document — the body of
// https://api.ssharcade.dev/v1/players. See docs/07-live-player-count.md.
type Snapshot struct {
	Version         int            `json:"version"`
	Online          bool           `json:"online"`
	UpdatedAt       time.Time      `json:"updated_at"`
	IntervalSeconds int            `json:"interval_seconds"`
	Players         int            `json:"players"`
	Sessions        int            `json:"sessions"`
	InLobby         int            `json:"in_lobby"`
	Games           []GameSnapshot `json:"games"`
}

// GameSnapshot is one cabinet in menu order.
type GameSnapshot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"` // "online" | "offline"
	Players int    `json:"players"`
}

// GameLister is the slice of *registry.Registry the publisher reads.
type GameLister interface {
	Games() []registry.GameStatus
}

// Options configures a Publisher. Path and Interval are required.
type Options struct {
	Path     string        // file to (re)write atomically
	Interval time.Duration // how often; readers treat ~3× this as stale
	Logger   *slog.Logger
	Now      func() time.Time // injectable clock for tests
}

// Publisher rewrites the snapshot file on a fixed interval until closed,
// then writes a final offline snapshot so readers see the arcade go down
// rather than a frozen count.
type Publisher struct {
	tracker *Tracker
	games   GameLister
	opts    Options

	stop      chan struct{}
	done      chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	failing   bool // only touched by the loop goroutine and Close after it exits
}

// NewPublisher validates opts and prepares the output directory.
func NewPublisher(t *Tracker, games GameLister, opts Options) (*Publisher, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("presence: empty snapshot path")
	}
	if opts.Interval < time.Second {
		return nil, fmt.Errorf("presence: interval must be at least 1s, got %s", opts.Interval)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return nil, fmt.Errorf("presence: create snapshot dir: %w", err)
	}
	return &Publisher{
		tracker: t,
		games:   games,
		opts:    opts,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}, nil
}

// Start writes the first snapshot immediately, then one per interval.
func (p *Publisher) Start() {
	p.startOnce.Do(func() {
		p.publish(true)
		go p.loop()
	})
}

// Close stops the loop and writes the final offline snapshot. Safe to call
// more than once, and without Start.
func (p *Publisher) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		// Never started: no loop will close done, so close it here. This also
		// turns a later Start into a no-op.
		p.startOnce.Do(func() { close(p.done) })
		<-p.done
		p.publish(false)
	})
}

func (p *Publisher) loop() {
	defer close(p.done)
	tick := time.NewTicker(p.opts.Interval)
	defer tick.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
			p.publish(true)
		}
	}
}

// Build assembles the snapshot for the current moment. online=false is the
// shutdown document: zero counts and every cabinet offline, because with
// the router gone nothing on the floor is reachable.
func (p *Publisher) Build(online bool) Snapshot {
	snap := Snapshot{
		Version:         SchemaVersion,
		Online:          online,
		UpdatedAt:       p.opts.Now().UTC().Truncate(time.Second),
		IntervalSeconds: int(p.opts.Interval / time.Second),
		Games:           []GameSnapshot{},
	}
	var c Counts
	if online {
		c = p.tracker.Counts()
		snap.Players, snap.Sessions, snap.InLobby = c.Players, c.Sessions, c.InLobby
	}
	for _, g := range p.games.Games() {
		gs := GameSnapshot{ID: g.ID, Name: g.Name, Status: "offline"}
		if online {
			gs.Players = c.Games[g.ID]
			if g.Online {
				gs.Status = "online"
			}
		}
		snap.Games = append(snap.Games, gs)
	}
	return snap
}

func (p *Publisher) publish(online bool) {
	err := p.write(p.Build(online))
	switch {
	case err != nil && !p.failing:
		// Log the transition, not every tick — a full disk would otherwise
		// log once per interval forever.
		p.failing = true
		p.opts.Logger.Warn("player-count snapshot write failed", "path", p.opts.Path, "error", err)
	case err == nil && p.failing:
		p.failing = false
		p.opts.Logger.Info("player-count snapshot writes recovered", "path", p.opts.Path)
	}
}

// write replaces the file atomically: a reader (Caddy) sees the old
// document or the new one, never a torn write. The temp file lives in the
// same directory so the rename cannot cross filesystems, and is dot-named
// so it can never be served by the Caddy rewrite, which names the final
// file explicitly.
func (p *Publisher) write(snap Snapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir, base := filepath.Split(p.opts.Path)
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after a successful rename

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	// CreateTemp makes the file 0600. Caddy reads it as root but with every
	// capability dropped — no CAP_DAC_OVERRIDE — so it gets ordinary
	// permission checks against a file owned by uid 65532. World-readable
	// is required, and harmless: the content is public by design.
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p.opts.Path)
}

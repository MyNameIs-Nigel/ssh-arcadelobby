// Package registry knows what games exist (games.toml) and which are alive
// (the health prober). Adding a game to the arcade must never require a
// router rebuild: the file is re-checked every probe cycle and on SIGHUP,
// and a file that fails validation is rejected as a whole — the previous
// good registry stays active.
package registry

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	gossh "golang.org/x/crypto/ssh"
)

// Game is one games.toml entry. Snapshots handed out by the registry are
// immutable copies — mutating one never affects the registry.
type Game struct {
	ID          string
	Name        string
	Tagline     string
	Addr        string
	Descriptors []string
	HostKey     gossh.PublicKey // nil = unpinned (accept any on the private net)
	Order       int
}

// GameStatus is a Game plus its probed liveness.
type GameStatus struct {
	Game
	Online     bool
	LastChange time.Time
}

// Options tune the prober; zero values take the documented defaults.
type Options struct {
	ProbeInterval time.Duration // default 15s
	DialTimeout   time.Duration // default 2s (TCP dial and banner read)
	FailThreshold int           // consecutive failures before Offline; default 2
	Logger        *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.ProbeInterval <= 0 {
		o.ProbeInterval = 15 * time.Second
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 2 * time.Second
	}
	if o.FailThreshold <= 0 {
		o.FailThreshold = 2
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

type health struct {
	online     bool
	fails      int
	lastChange time.Time
}

// Registry loads games.toml, hot-reloads it, probes game liveness, and
// notifies subscribers on any status or registry change.
type Registry struct {
	path string
	opts Options

	mu     sync.RWMutex
	games  []Game
	health map[string]*health
	subs   map[chan struct{}]struct{}
	mtime  time.Time

	kick       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	started    bool
	startOnce  sync.Once
	closeOnce  sync.Once
	stopSighup func()
}

// New loads and validates the file. At boot there is no previous good
// registry to fall back to, so a bad file is a hard error.
func New(path string, opts Options) (*Registry, error) {
	r := &Registry{
		path:   path,
		opts:   opts.withDefaults(),
		health: make(map[string]*health),
		subs:   make(map[chan struct{}]struct{}),
		kick:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	games, mtime, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	r.games, r.mtime = games, mtime
	now := time.Now()
	for _, g := range games {
		r.health[g.ID] = &health{lastChange: now}
	}
	return r, nil
}

// Start launches the probe/reload loop. The first cycle runs immediately.
func (r *Registry) Start() {
	r.startOnce.Do(func() {
		// SIGHUP forces a reload+probe cycle for impatient operators. The
		// registration is a no-op on Windows dev hosts (see sighup_windows.go).
		r.stopSighup = watchSighup(r.ProbeNow)
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		go r.loop()
	})
}

// Close stops the loop and waits for it; subscriber channels are closed.
func (r *Registry) Close() {
	r.closeOnce.Do(func() {
		if r.stopSighup != nil {
			r.stopSighup()
		}
		close(r.stop)
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		if started {
			<-r.done
		}
		r.mu.Lock()
		for ch := range r.subs {
			delete(r.subs, ch)
			close(ch)
		}
		r.mu.Unlock()
	})
}

// Games returns an immutable snapshot, sorted for the menu (Order, then ID).
func (r *Registry) Games() []GameStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]GameStatus, 0, len(r.games))
	for _, g := range r.games {
		st := GameStatus{Game: g}
		st.Descriptors = append([]string(nil), g.Descriptors...)
		if h := r.health[g.ID]; h != nil {
			st.Online = h.online
			st.LastChange = h.lastChange
		}
		out = append(out, st)
	}
	return out
}

// Subscribe returns a channel poked (coalesced) on any status or registry
// change. Callers pull fresh snapshots via Games. Unsubscribe closes it.
func (r *Registry) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a channel returned by Subscribe.
func (r *Registry) Unsubscribe(ch chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[ch]; ok {
		delete(r.subs, ch)
		close(ch)
	}
}

// ProbeNow schedules an immediate reload-check + probe cycle (the lobby's
// R key, SIGHUP). Non-blocking; cycles coalesce.
func (r *Registry) ProbeNow() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Report lets the bridge flag a game that died between probes: a dial
// failure flips it Offline immediately instead of waiting out the damping.
func (r *Registry) Report(id string, err error) {
	r.mu.Lock()
	h, ok := r.health[id]
	changed := false
	if ok {
		if h.fails < r.opts.FailThreshold {
			h.fails = r.opts.FailThreshold
		}
		if h.online {
			h.online = false
			h.lastChange = time.Now()
			changed = true
		}
	}
	r.mu.Unlock()
	if changed {
		r.opts.Logger.Warn("game reported down", "game", id, "error", err)
		r.notify()
	}
}

func (r *Registry) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.opts.ProbeInterval)
	defer ticker.Stop()
	for {
		r.reloadIfChanged()
		r.probeCycle()
		select {
		case <-r.stop:
			return
		case <-ticker.C:
		case <-r.kick:
		}
	}
}

// reloadIfChanged re-stats the file (simplest cross-platform choice — no
// fsnotify dependency) and swaps in a new snapshot on mtime change. A file
// that fails to load keeps the previous good registry active.
func (r *Registry) reloadIfChanged() {
	info, err := os.Stat(r.path)
	if err != nil {
		r.opts.Logger.Error("registry stat failed; keeping previous registry",
			"path", r.path, "error", err)
		return
	}
	r.mu.RLock()
	unchanged := info.ModTime().Equal(r.mtime)
	r.mu.RUnlock()
	if unchanged {
		return
	}

	games, mtime, err := loadFile(r.path)
	r.mu.Lock()
	// Remember the mtime even on failure so a bad file is logged once, not
	// every cycle; the next edit changes mtime again and gets re-checked.
	r.mtime = info.ModTime()
	if err != nil {
		r.mu.Unlock()
		r.opts.Logger.Error("registry reload rejected; keeping previous registry",
			"path", r.path, "error", err)
		return
	}
	r.mtime = mtime
	r.games = games
	now := time.Now()
	fresh := make(map[string]*health, len(games))
	for _, g := range games {
		if h, ok := r.health[g.ID]; ok {
			fresh[g.ID] = h
		} else {
			fresh[g.ID] = &health{lastChange: now}
		}
	}
	r.health = fresh
	r.mu.Unlock()

	r.opts.Logger.Info("registry reloaded", "path", r.path, "games", len(games))
	r.notify()
}

func (r *Registry) notify() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type fileGame struct {
	ID          string   `toml:"id"`
	Name        string   `toml:"name"`
	Tagline     string   `toml:"tagline"`
	Descriptors []string `toml:"descriptors"`
	Addr        string   `toml:"addr"`
	HostKey     string   `toml:"host_key"`
	Order       int      `toml:"order"`
}

type fileRoot struct {
	Games []fileGame `toml:"games"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)

func loadFile(path string) ([]Game, time.Time, error) {
	// Stat before read: if a write races the read, the recorded mtime
	// predates it and the next cycle re-checks.
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("registry: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("registry: %w", err)
	}
	var root fileRoot
	if err := toml.Unmarshal(raw, &root); err != nil {
		return nil, time.Time{}, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	games, err := validate(root.Games)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("registry: %s: %w", path, err)
	}
	return games, info.ModTime(), nil
}

func validate(entries []fileGame) ([]Game, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("no [[games]] entries")
	}
	seen := make(map[string]bool, len(entries))
	seenName := make(map[string]bool, len(entries))
	games := make([]Game, 0, len(entries))
	for i, e := range entries {
		if !idPattern.MatchString(e.ID) {
			return nil, fmt.Errorf("games[%d]: id %q must match %s", i, e.ID, idPattern)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("games[%d]: duplicate id %q", i, e.ID)
		}
		seen[e.ID] = true
		if e.Name == "" {
			return nil, fmt.Errorf("games[%d] (%s): name must not be empty", i, e.ID)
		}
		if seenName[e.Name] {
			return nil, fmt.Errorf("games[%d] (%s): duplicate name %q — two menu entries would be visually indistinguishable", i, e.ID, e.Name)
		}
		seenName[e.Name] = true
		if e.Addr == "" {
			return nil, fmt.Errorf("games[%d] (%s): addr must not be empty", i, e.ID)
		}
		if _, _, err := net.SplitHostPort(e.Addr); err != nil {
			return nil, fmt.Errorf("games[%d] (%s): addr %q: %w", i, e.ID, e.Addr, err)
		}
		g := Game{
			ID:          e.ID,
			Name:        e.Name,
			Tagline:     e.Tagline,
			Addr:        e.Addr,
			Descriptors: append([]string(nil), e.Descriptors...),
			Order:       e.Order,
		}
		if e.HostKey != "" {
			key, _, _, _, err := gossh.ParseAuthorizedKey([]byte(e.HostKey))
			if err != nil {
				return nil, fmt.Errorf("games[%d] (%s): host_key: %w", i, e.ID, err)
			}
			g.HostKey = key
		}
		games = append(games, g)
	}
	sort.SliceStable(games, func(a, b int) bool {
		if games[a].Order != games[b].Order {
			return games[a].Order < games[b].Order
		}
		return games[a].ID < games[b].ID
	})
	return games, nil
}

// Package banner loads operator-controlled lobby notices from banner.toml.
// The file is hot-reloaded on mtime change; a bad reload keeps the previous
// good notice. A missing or invalid file never takes the router down.
package banner

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// Level styles the notice strip in the lobby.
type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelDanger  Level = "danger"
)

// Notice is the operator banner shown above the game list.
type Notice struct {
	Enabled bool
	Level   Level
	Title   string
	Message string
}

// Options tune the reload loop; zero values take documented defaults.
type Options struct {
	ReloadInterval time.Duration // default 15s
	Logger         *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.ReloadInterval <= 0 {
		o.ReloadInterval = 15 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return o
}

// Source watches banner.toml and notifies subscribers on change.
type Source struct {
	path string
	opts Options

	mu     sync.RWMutex
	notice Notice
	mtime  time.Time
	subs   map[chan struct{}]struct{}

	kick      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	started   bool
}

// New loads the initial notice. Missing files and parse errors at boot log a
// warning and yield a disabled notice — the router must still start.
func New(path string, opts Options) (*Source, error) {
	s := &Source{
		path: path,
		opts: opts.withDefaults(),
		subs: make(map[chan struct{}]struct{}),
		kick: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	notice, mtime, err := loadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.opts.Logger.Info("banner file not found; notices disabled", "path", path)
		} else {
			s.opts.Logger.Warn("banner load failed; notices disabled", "path", path, "error", err)
		}
		s.notice = Notice{}
		return s, nil
	}
	s.notice, s.mtime = notice, mtime
	return s, nil
}

// Start launches the reload loop.
func (s *Source) Start() {
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.started = true
		s.mu.Unlock()
		go s.loop()
	})
}

// Close stops the loop and closes subscriber channels.
func (s *Source) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		started := s.started
		s.mu.Unlock()
		if started {
			close(s.stop)
			<-s.done
		}
		s.mu.Lock()
		for ch := range s.subs {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	})
}

// Notice returns the current banner snapshot.
func (s *Source) Notice() Notice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notice
}

// Subscribe returns a channel poked on notice changes.
func (s *Source) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a channel returned by Subscribe.
func (s *Source) Unsubscribe(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
}

// ReloadNow schedules an immediate reload check.
func (s *Source) ReloadNow() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Source) loop() {
	defer close(s.done)
	ticker := time.NewTicker(s.opts.ReloadInterval)
	defer ticker.Stop()
	for {
		s.reloadIfChanged()
		select {
		case <-s.stop:
			return
		case <-ticker.C:
		case <-s.kick:
		}
	}
}

func (s *Source) reloadIfChanged() {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			unchanged := s.notice == (Notice{}) && s.mtime.IsZero()
			if !unchanged {
				s.notice = Notice{}
				s.mtime = time.Time{}
				s.mu.Unlock()
				s.opts.Logger.Info("banner file removed; notices disabled", "path", s.path)
				s.notify()
			} else {
				s.mu.Unlock()
			}
			return
		}
		s.opts.Logger.Error("banner stat failed; keeping previous notice",
			"path", s.path, "error", err)
		return
	}

	s.mu.RLock()
	unchanged := info.ModTime().Equal(s.mtime)
	s.mu.RUnlock()
	if unchanged {
		return
	}

	notice, mtime, err := loadFile(s.path)
	s.mu.Lock()
	s.mtime = info.ModTime()
	if err != nil {
		s.mu.Unlock()
		s.opts.Logger.Error("banner reload rejected; keeping previous notice",
			"path", s.path, "error", err)
		return
	}
	s.mtime = mtime
	changed := s.notice != notice
	s.notice = notice
	s.mu.Unlock()
	if changed {
		s.opts.Logger.Info("banner reloaded", "path", s.path, "enabled", notice.Enabled)
		s.notify()
	}
}

func (s *Source) notify() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type fileRoot struct {
	Enabled bool   `toml:"enabled"`
	Level   string `toml:"level"`
	Title   string `toml:"title"`
	Message string `toml:"message"`
}

const (
	maxTitleLen   = 48
	maxMessageLen = 240
)

func loadFile(path string) (Notice, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Notice{}, time.Time{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Notice{}, time.Time{}, fmt.Errorf("banner: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return Notice{}, time.Time{}, fmt.Errorf("banner: %s: empty file", path)
	}
	var root fileRoot
	if err := toml.Unmarshal(raw, &root); err != nil {
		return Notice{}, time.Time{}, fmt.Errorf("banner: parse %s: %w", path, err)
	}
	notice, err := validate(root)
	if err != nil {
		return Notice{}, time.Time{}, fmt.Errorf("banner: %s: %w", path, err)
	}
	return notice, info.ModTime(), nil
}

func validate(root fileRoot) (Notice, error) {
	if !root.Enabled {
		return Notice{}, nil
	}
	level := Level(strings.ToLower(strings.TrimSpace(root.Level)))
	if level == "" {
		level = LevelInfo
	}
	switch level {
	case LevelInfo, LevelWarning, LevelDanger:
	default:
		return Notice{}, fmt.Errorf("level %q must be info, warning, or danger", root.Level)
	}
	title := strings.TrimSpace(root.Title)
	message := strings.TrimSpace(root.Message)
	if title == "" && message == "" {
		return Notice{}, fmt.Errorf("enabled banner needs title and/or message")
	}
	if len(title) > maxTitleLen {
		return Notice{}, fmt.Errorf("title exceeds %d characters", maxTitleLen)
	}
	if len(message) > maxMessageLen {
		return Notice{}, fmt.Errorf("message exceeds %d characters", maxMessageLen)
	}
	return Notice{
		Enabled: true,
		Level:   level,
		Title:   title,
		Message: message,
	}, nil
}

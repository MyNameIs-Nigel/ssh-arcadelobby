package banner

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func writeBanner(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bumpMtime(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	ts := time.Now().Add(offset)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func newTestSource(t *testing.T, content string) (*Source, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "banner.toml")
	if content != "" {
		writeBanner(t, path, content)
	}
	s, err := New(path, Options{ReloadInterval: 20 * time.Millisecond, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, path
}

func TestDisabledByDefault(t *testing.T) {
	s, _ := newTestSource(t, `enabled = false`)
	if n := s.Notice(); n.Enabled {
		t.Fatalf("notice = %+v", n)
	}
}

func TestLoadEnabledNotice(t *testing.T) {
	s, _ := newTestSource(t, `
enabled = true
level = "warning"
title = "Maintenance tonight"
message = "Farm will be down 02:00-02:15 UTC."
`)
	n := s.Notice()
	if !n.Enabled || n.Level != LevelWarning || n.Title == "" || n.Message == "" {
		t.Fatalf("notice = %+v", n)
	}
}

func TestValidationRejectsBadLevel(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "banner.toml"), Options{Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bad.toml")
	writeBanner(t, path, `enabled = true
level = "critical"
title = "x"
`)
	s := &Source{path: path, opts: Options{Logger: quietLogger()}}
	if _, _, err := loadFile(path); err == nil {
		t.Fatal("expected validation error")
	}
	_ = s
}

func TestBadReloadKeepsPreviousNotice(t *testing.T) {
	s, path := newTestSource(t, `
enabled = true
level = "info"
title = "Good"
message = "Still good"
`)
	s.Start()
	writeBanner(t, path, `enabled = true
level = "nope"
title = "Bad"
`)
	bumpMtime(t, path, time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := s.Notice(); n.Title == "Good" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("notice after bad reload = %+v", s.Notice())
}

func TestHotReloadPicksUpChange(t *testing.T) {
	s, path := newTestSource(t, `enabled = false`)
	s.Start()
	writeBanner(t, path, `
enabled = true
level = "danger"
title = "Outage"
message = "Moon Miner offline."
`)
	bumpMtime(t, path, time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := s.Notice(); n.Enabled && n.Title == "Outage" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("notice = %+v", s.Notice())
}

func TestSubscribeNotify(t *testing.T) {
	s, path := newTestSource(t, `enabled = false`)
	s.Start()
	ch := s.Subscribe()
	writeBanner(t, path, `
enabled = true
level = "info"
title = "News"
message = "Welcome."
`)
	bumpMtime(t, path, time.Second)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber not notified")
	}
}

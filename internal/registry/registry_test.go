package registry

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const goodTOML = `
[[games]]
id = "beta"
name = "BETA"
tagline = "Second by order."
descriptors = ["b"]
addr = "beta:22"
order = 20

[[games]]
id = "alpha"
name = "ALPHA"
tagline = "First by order."
descriptors = ["a", "aa"]
addr = "alpha:22"
order = 10
`

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func writeGames(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bumpMtime guarantees the reloader sees a change regardless of filesystem
// timestamp granularity.
func bumpMtime(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	ts := time.Now().Add(offset)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

func newTestRegistry(t *testing.T, content string, opts Options) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "games.toml")
	writeGames(t, path, content)
	if opts.Logger == nil {
		opts.Logger = quietLogger()
	}
	r, err := New(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, path
}

func TestLoadValidFileSortsByOrder(t *testing.T) {
	r, _ := newTestRegistry(t, goodTOML, Options{})
	games := r.Games()
	if len(games) != 2 {
		t.Fatalf("got %d games, want 2", len(games))
	}
	if games[0].ID != "alpha" || games[1].ID != "beta" {
		t.Fatalf("wrong order: %s, %s", games[0].ID, games[1].ID)
	}
	if games[0].Online {
		t.Fatal("games must start offline until probed")
	}
	if games[0].Name != "ALPHA" || len(games[0].Descriptors) != 2 {
		t.Fatalf("fields not loaded: %+v", games[0])
	}
}

func TestVersionLoadsAndIsOptional(t *testing.T) {
	r, _ := newTestRegistry(t, `
[[games]]
id = "versioned"
name = "VERSIONED"
addr = "v:22"
order = 10
version = "2.1.3"

[[games]]
id = "unversioned"
name = "UNVERSIONED"
addr = "u:22"
order = 20
`, Options{})
	games := r.Games()
	if games[0].Version != "2.1.3" {
		t.Fatalf("version not loaded: %+v", games[0])
	}
	if games[1].Version != "" {
		t.Fatalf("omitted version must stay empty: %+v", games[1])
	}
}

func TestChannel(t *testing.T) {
	cases := []struct{ version, want string }{
		{"1.0.0", "alpha"},
		{"1.4.2", "alpha"},
		{"2.0.0", "beta"},
		{"2.10.1", "beta"},
		{"3.0.0", ""}, // no channel defined yet past beta
		{"", ""},
		{"10.0.0", ""}, // channel is the whole leading number, not its first digit
	}
	for _, tc := range cases {
		if got := Channel(tc.version); got != tc.want {
			t.Errorf("Channel(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

func TestValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		toml string
	}{
		{"no games", "# empty\n"},
		{"empty id", "[[games]]\nid = \"\"\nname = \"X\"\naddr = \"x:22\"\n"},
		{"bad id chars", "[[games]]\nid = \"Bad_ID\"\nname = \"X\"\naddr = \"x:22\"\n"},
		{"id too long", "[[games]]\nid = \"" + strings.Repeat("a", 25) + "\"\nname = \"X\"\naddr = \"x:22\"\n"},
		{"duplicate id", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\n[[games]]\nid = \"x\"\nname = \"Y\"\naddr = \"y:22\"\n"},
		{"duplicate name", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\n[[games]]\nid = \"y\"\nname = \"X\"\naddr = \"y:22\"\n"},
		{"empty name", "[[games]]\nid = \"x\"\nname = \"\"\naddr = \"x:22\"\n"},
		{"empty addr", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"\"\n"},
		{"addr without port", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"gamehost\"\n"},
		{"bad host_key", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\nhost_key = \"not a key\"\n"},
		{"version not three parts", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\nversion = \"1.0\"\n"},
		{"version with prefix", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\nversion = \"v1.0.0\"\n"},
		{"version non-numeric", "[[games]]\nid = \"x\"\nname = \"X\"\naddr = \"x:22\"\nversion = \"beta.0.0\"\n"},
		{"toml syntax", "[[games]\nid = \"x\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "games.toml")
			writeGames(t, path, tc.toml)
			if _, err := New(path, Options{Logger: quietLogger()}); err == nil {
				t.Fatalf("New accepted invalid file (%s)", tc.name)
			}
		})
	}
}

func TestBadReloadKeepsPreviousRegistry(t *testing.T) {
	r, path := newTestRegistry(t, goodTOML, Options{})

	writeGames(t, path, "[[games]]\nid = \"\"\n")
	bumpMtime(t, path, time.Hour)
	r.reloadIfChanged()

	games := r.Games()
	if len(games) != 2 || games[0].ID != "alpha" {
		t.Fatalf("bad reload replaced registry: %+v", games)
	}

	// A subsequent good write is picked up.
	writeGames(t, path, goodTOML+`
[[games]]
id = "gamma"
name = "GAMMA"
addr = "gamma:22"
order = 30
`)
	bumpMtime(t, path, 2*time.Hour)
	r.reloadIfChanged()
	if got := len(r.Games()); got != 3 {
		t.Fatalf("good reload after bad not applied: %d games", got)
	}
}

func TestReloadPreservesHealth(t *testing.T) {
	r, path := newTestRegistry(t, goodTOML, Options{})
	r.mu.Lock()
	r.health["alpha"].online = true
	r.mu.Unlock()

	writeGames(t, path, goodTOML)
	bumpMtime(t, path, time.Hour)
	r.reloadIfChanged()

	for _, g := range r.Games() {
		if g.ID == "alpha" && !g.Online {
			t.Fatal("reload reset alpha's health")
		}
	}
}

func TestHotReloadPicksUpAddedGameWithinOneCycle(t *testing.T) {
	r, path := newTestRegistry(t, goodTOML, Options{
		ProbeInterval: 30 * time.Millisecond,
		DialTimeout:   50 * time.Millisecond,
	})
	sub := r.Subscribe()
	r.Start()

	writeGames(t, path, goodTOML+`
[[games]]
id = "gamma"
name = "GAMMA"
addr = "127.0.0.1:1"
order = 30
`)
	bumpMtime(t, path, time.Hour)

	deadline := time.After(5 * time.Second)
	for {
		if len(r.Games()) == 3 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("added game not picked up; have %d", len(r.Games()))
		case <-sub:
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestSubscribeNotifyAndUnsubscribe(t *testing.T) {
	r, _ := newTestRegistry(t, goodTOML, Options{})
	sub := r.Subscribe()
	r.notify()
	select {
	case <-sub:
	default:
		t.Fatal("no poke after notify")
	}
	// Coalescing: many notifies, at most one pending poke.
	r.notify()
	r.notify()
	<-sub
	select {
	case <-sub:
		t.Fatal("pokes did not coalesce")
	default:
	}

	r.Unsubscribe(sub)
	if _, ok := <-sub; ok {
		t.Fatal("unsubscribed channel not closed")
	}
}

func TestConcurrentAccessUnderLoad(t *testing.T) {
	r, path := newTestRegistry(t, goodTOML, Options{
		ProbeInterval: 10 * time.Millisecond,
		DialTimeout:   20 * time.Millisecond,
	})
	r.Start()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = r.Games()
				r.ProbeNow()
				sub := r.Subscribe()
				r.Unsubscribe(sub)
				r.Report("alpha", fmt.Errorf("synthetic"))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			writeGames(t, path, goodTOML)
			bumpMtime(t, path, time.Duration(i)*time.Second)
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestNoGoroutineLeaksAfterClose(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		r, _ := newTestRegistry(t, goodTOML, Options{
			ProbeInterval: 10 * time.Millisecond,
			DialTimeout:   20 * time.Millisecond,
		})
		_ = r.Subscribe()
		r.Start()
		r.ProbeNow()
		r.Close()
	}

	// Settle-retry: probe goroutines from the last cycle need a moment.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutines: before=%d after=%d", before, runtime.NumGoroutine())
}

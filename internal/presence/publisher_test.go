package presence

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

type fakeGames []registry.GameStatus

func (f fakeGames) Games() []registry.GameStatus { return f }

func testGames() fakeGames {
	return fakeGames{
		{Game: registry.Game{ID: "farm", Name: "IDLE FARMER"}, Online: true},
		{Game: registry.Game{ID: "moonminer", Name: "MOON MINER"}, Online: true},
		{Game: registry.Game{ID: "chess", Name: "GAMBIT"}, Online: false},
	}
}

func testPublisher(t *testing.T, tr *Tracker, interval time.Duration) (*Publisher, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stats", "players.json") // dir created by NewPublisher
	p, err := NewPublisher(tr, testGames(), Options{
		Path:     path,
		Interval: interval,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return time.Date(2026, 9, 25, 18, 4, 5, 999, time.FixedZone("x", 3600)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, path
}

func readSnapshot(t *testing.T, path string) Snapshot {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("snapshot is not JSON: %v\n%s", err, raw)
	}
	return s
}

func TestPublishWritesOnlineSnapshot(t *testing.T) {
	tr := NewTracker()
	tr.Join("alice").Enter("farm")
	tr.Join("bob").Enter("farm")
	tr.Join("carol")

	p, path := testPublisher(t, tr, 5*time.Second)
	p.Start()
	t.Cleanup(p.Close)

	s := readSnapshot(t, path)
	if s.Version != SchemaVersion || !s.Online || s.IntervalSeconds != 5 {
		t.Fatalf("header = %+v", s)
	}
	if want := time.Date(2026, 9, 25, 17, 4, 5, 0, time.UTC); !s.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %s, want %s (UTC, whole seconds)", s.UpdatedAt, want)
	}
	if s.Players != 3 || s.Sessions != 3 || s.InLobby != 1 {
		t.Errorf("counts = players %d sessions %d lobby %d", s.Players, s.Sessions, s.InLobby)
	}
	want := []GameSnapshot{
		{ID: "farm", Name: "IDLE FARMER", Status: "online", Players: 2},
		{ID: "moonminer", Name: "MOON MINER", Status: "online", Players: 0},
		{ID: "chess", Name: "GAMBIT", Status: "offline", Players: 0},
	}
	if len(s.Games) != len(want) {
		t.Fatalf("games = %+v", s.Games)
	}
	for i := range want {
		if s.Games[i] != want[i] {
			t.Errorf("games[%d] = %+v, want %+v", i, s.Games[i], want[i])
		}
	}
}

func TestSnapshotFieldNames(t *testing.T) {
	// The JSON keys are the public API contract (docs/07). Renaming a Go
	// field must not silently rename a key.
	p, _ := testPublisher(t, NewTracker(), 5*time.Second)
	raw, err := json.Marshal(p.Build(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"version":`, `"online":`, `"updated_at":"2026-09-25T17:04:05Z"`, `"interval_seconds":`,
		`"players":`, `"sessions":`, `"in_lobby":`, `"games":[`, `"id":"farm"`, `"name":`, `"status":"online"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("snapshot JSON missing %s:\n%s", key, raw)
		}
	}
}

func TestSnapshotIsWorldReadable(t *testing.T) {
	// Caddy reads the file as a capability-less root: without o+r it gets
	// permission denied and the API 403s.
	p, path := testPublisher(t, NewTracker(), 5*time.Second)
	p.Start()
	t.Cleanup(p.Close)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o004 == 0 {
		t.Fatalf("snapshot mode %v is not world-readable", fi.Mode().Perm())
	}
}

func TestCloseWritesOfflineSnapshotAndLeavesNoTempFiles(t *testing.T) {
	tr := NewTracker()
	tr.Join("alice").Enter("farm")

	p, path := testPublisher(t, tr, time.Second)
	p.Start()
	p.Close()
	p.Close() // idempotent

	s := readSnapshot(t, path)
	if s.Online || s.Players != 0 || s.Sessions != 0 || s.InLobby != 0 {
		t.Fatalf("offline snapshot still reports players: %+v", s)
	}
	for _, g := range s.Games {
		if g.Status != "offline" || g.Players != 0 {
			t.Errorf("offline snapshot game %+v", g)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only players.json, found %v", names)
	}
}

func TestCloseWithoutStart(t *testing.T) {
	p, path := testPublisher(t, NewTracker(), time.Second)
	p.Close()
	if s := readSnapshot(t, path); s.Online {
		t.Fatalf("expected offline snapshot, got %+v", s)
	}
	p.Start() // no-op after Close; must not panic or write online
	if s := readSnapshot(t, path); s.Online {
		t.Fatal("Start after Close published an online snapshot")
	}
}

func TestPublisherRefreshesOnInterval(t *testing.T) {
	tr := NewTracker()
	p, path := testPublisher(t, tr, time.Second)
	p.Start()
	t.Cleanup(p.Close)

	if s := readSnapshot(t, path); s.Players != 0 {
		t.Fatalf("initial players = %d", s.Players)
	}
	seat := tr.Join("alice")
	defer seat.Leave()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if readSnapshot(t, path).Players == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("snapshot never picked up the new player")
}

func TestNewPublisherValidates(t *testing.T) {
	if _, err := NewPublisher(NewTracker(), testGames(), Options{Interval: time.Second}); err == nil {
		t.Error("empty path accepted")
	}
	path := filepath.Join(t.TempDir(), "players.json")
	if _, err := NewPublisher(NewTracker(), testGames(), Options{Path: path, Interval: 500 * time.Millisecond}); err == nil {
		t.Error("sub-second interval accepted")
	}
}

package lobby

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

type fakeSource struct {
	games  []registry.GameStatus
	probes int
}

func (f *fakeSource) Games() []registry.GameStatus { return f.games }
func (f *fakeSource) ProbeNow()                    { f.probes++ }

func twoGames() *fakeSource {
	return &fakeSource{games: []registry.GameStatus{
		{Game: registry.Game{ID: "moon", Name: "MOON MINER", Tagline: "Drill.", Descriptors: []string{"x"}}, Online: true},
		{Game: registry.Game{ID: "farm", Name: "IDLE FARMER", Tagline: "Grow.", Descriptors: []string{"y"}}, Online: false},
	}}
}

type fixture struct {
	src   *fakeSource
	now   time.Time
	model Model
}

func newFixture(t *testing.T, src *fakeSource, opts ...Option) *fixture {
	t.Helper()
	f := &fixture{src: src, now: time.Unix(1_700_000_000, 0)}
	opts = append(opts, WithClock(func() time.Time { return f.now }))
	f.model = New(src, nil, "SHA256:testfingerprint", 80, 24, 5*time.Minute, opts...)
	return f
}

func (f *fixture) update(t *testing.T, msg tea.Msg) tea.Cmd {
	t.Helper()
	next, cmd := f.model.Update(msg)
	m, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	f.model = m
	return cmd
}

func (f *fixture) press(t *testing.T, key string) tea.Cmd {
	t.Helper()
	var msg tea.KeyPressMsg
	switch key {
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		msg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	return f.update(t, msg)
}

// isQuit reports whether cmd (possibly a batch) yields tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if isQuit(c) {
				return true
			}
		}
	}
	return false
}

func TestCursorMovementClamps(t *testing.T) {
	f := newFixture(t, twoGames())
	if f.model.cursor != 0 {
		t.Fatal("cursor should start at 0")
	}
	f.press(t, "up")
	if f.model.cursor != 0 {
		t.Fatal("up at top moved the cursor")
	}
	f.press(t, "down")
	if f.model.cursor != 1 {
		t.Fatal("down did not move")
	}
	f.press(t, "down")
	if f.model.cursor != 1 {
		t.Fatal("down at bottom moved the cursor")
	}
	f.press(t, "k") // vim aliases
	if f.model.cursor != 0 {
		t.Fatal("k did not move up")
	}
	f.press(t, "j")
	if f.model.cursor != 1 {
		t.Fatal("j did not move down")
	}
}

func TestEnterOnOnlineGameQuitsWithPlayResult(t *testing.T) {
	f := newFixture(t, twoGames())
	cmd := f.press(t, "enter")
	if !isQuit(cmd) {
		t.Fatal("enter on online game did not quit the program")
	}
	res := f.model.Result()
	if res.Kind != KindPlay || res.Game.ID != "moon" {
		t.Fatalf("result = %+v", res)
	}
}

func TestSpaceActivatesToo(t *testing.T) {
	f := newFixture(t, twoGames())
	if cmd := f.press(t, "space"); !isQuit(cmd) {
		t.Fatal("space did not activate")
	}
}

func TestEnterOnOfflineGameFlashes(t *testing.T) {
	f := newFixture(t, twoGames())
	f.press(t, "down")
	cmd := f.press(t, "enter")
	if isQuit(cmd) {
		t.Fatal("enter on offline game quit the program")
	}
	if !strings.Contains(f.model.flash, "OFFLINE") {
		t.Fatalf("flash = %q, want offline notice", f.model.flash)
	}
	if f.model.Result().Kind != KindNone {
		t.Fatal("offline enter set a result")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		f := newFixture(t, twoGames())
		if cmd := f.press(t, key); !isQuit(cmd) {
			t.Fatalf("%s did not quit", key)
		}
		if f.model.Result().Kind != KindQuit {
			t.Fatalf("%s: result = %+v", key, f.model.Result())
		}
	}
}

func TestRefreshKeyProbes(t *testing.T) {
	f := newFixture(t, twoGames())
	f.press(t, "r")
	if f.src.probes != 1 {
		t.Fatalf("probes = %d, want 1", f.src.probes)
	}
}

func TestAboutOverlayCapturesInput(t *testing.T) {
	f := newFixture(t, twoGames())
	f.press(t, "?")
	if !f.model.aboutOpen {
		t.Fatal("? did not open about")
	}
	view := renderPlain(f.model)
	if !strings.Contains(view, "ABOUT SSHARCADE") {
		t.Fatal("about text not rendered")
	}
	// While open, navigation keys only close the overlay.
	f.press(t, "down")
	if f.model.aboutOpen {
		t.Fatal("key press did not close about")
	}
	if f.model.cursor != 0 {
		t.Fatal("overlay leaked the key to the menu")
	}
}

func TestWheelMovesSelection(t *testing.T) {
	f := newFixture(t, twoGames())
	f.update(t, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if f.model.cursor != 1 {
		t.Fatal("wheel down did not move selection")
	}
	f.update(t, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if f.model.cursor != 0 {
		t.Fatal("wheel up did not move selection")
	}
}

// clickOn renders (rebuilding hitboxes) and clicks the given game row.
func (f *fixture) clickOn(t *testing.T, idx int) tea.Cmd {
	t.Helper()
	_ = f.model.View()
	var target Box
	found := false
	for _, b := range f.model.hits.boxes {
		if d, ok := b.Data.(int); ok && d == idx {
			target = b
			found = true
		}
	}
	if !found {
		t.Fatalf("no hitbox for game %d", idx)
	}
	return f.update(t, tea.MouseClickMsg{X: target.X + 1, Y: target.Y, Button: tea.MouseLeft})
}

func TestClickSelectsThenActivates(t *testing.T) {
	f := newFixture(t, twoGames())
	f.src.games[1].Online = true

	// First click on an unselected row selects it.
	if cmd := f.clickOn(t, 1); isQuit(cmd) {
		t.Fatal("first click activated instead of selecting")
	}
	if f.model.cursor != 1 {
		t.Fatal("click did not select")
	}
	// Click on the already-selected row activates. (Well past the
	// double-click window, to prove selected-click alone activates.)
	f.now = f.now.Add(5 * time.Second)
	if cmd := f.clickOn(t, 1); !isQuit(cmd) {
		t.Fatal("click on selected row did not activate")
	}
	if f.model.Result().Game.ID != "farm" {
		t.Fatalf("activated %q", f.model.Result().Game.ID)
	}
}

func TestDoubleClickActivates(t *testing.T) {
	f := newFixture(t, twoGames())
	f.src.games[1].Online = true
	f.clickOn(t, 1)  // click 1: selects row 1
	f.press(t, "up") // selection moved off row 1 — a plain click would only re-select
	f.now = f.now.Add(300 * time.Millisecond)
	if cmd := f.clickOn(t, 1); !isQuit(cmd) {
		t.Fatal("double click within 400ms did not activate")
	}
}

func TestDoubleClickWindowExpires(t *testing.T) {
	f := newFixture(t, twoGames())
	f.src.games[1].Online = true
	f.clickOn(t, 1)
	f.press(t, "up")
	f.now = f.now.Add(time.Second) // past the 400ms window
	if cmd := f.clickOn(t, 1); isQuit(cmd) {
		t.Fatal("slow second click still counted as double click")
	}
}

func TestIdleTimeoutQuits(t *testing.T) {
	f := newFixture(t, twoGames())
	f.now = f.now.Add(5*time.Minute + time.Second)
	cmd := f.update(t, tickMsg(f.now))
	if !isQuit(cmd) {
		t.Fatal("idle timeout did not quit")
	}
	if f.model.Result().Kind != KindIdle {
		t.Fatalf("result = %+v", f.model.Result())
	}
}

func TestInputResetsIdleClock(t *testing.T) {
	f := newFixture(t, twoGames())
	f.now = f.now.Add(4 * time.Minute)
	f.press(t, "down")
	f.now = f.now.Add(4 * time.Minute) // 8m total, but input 4m ago
	if cmd := f.update(t, tickMsg(f.now)); isQuit(cmd) {
		t.Fatal("idle fired despite recent input")
	}
}

func TestFlashExpires(t *testing.T) {
	f := newFixture(t, twoGames(), WithFlash("TEST NOTICE"))
	if !strings.Contains(renderPlain(f.model), "TEST NOTICE") {
		t.Fatal("flash not rendered")
	}
	f.now = f.now.Add(4 * time.Second)
	f.update(t, tickMsg(f.now))
	if strings.Contains(renderPlain(f.model), "TEST NOTICE") {
		t.Fatal("flash did not expire")
	}
}

func TestPokeRefreshesGames(t *testing.T) {
	f := newFixture(t, twoGames())
	f.src.games[1].Online = true
	f.update(t, pokeMsg{})
	if !f.model.games[1].Online {
		t.Fatal("poke did not refresh game statuses")
	}
}

func TestTickRefreshesGamesToo(t *testing.T) {
	f := newFixture(t, twoGames())
	f.src.games = f.src.games[:1]
	f.press(t, "down") // cursor to 1, about to become invalid
	f.update(t, tickMsg(f.now))
	if len(f.model.games) != 1 || f.model.cursor != 0 {
		t.Fatalf("tick refresh: games=%d cursor=%d", len(f.model.games), f.model.cursor)
	}
}

func TestViewShowsMenu(t *testing.T) {
	f := newFixture(t, twoGames())
	view := renderPlain(f.model)
	for _, want := range []string{
		"SSHARCADE",
		"play.ssharcade.dev",
		"MOON MINER",
		"IDLE FARMER",
		"Drill.",
		"OFFLINE — back soon", // offline game's tagline is replaced
		"Your key is your account",
		"ENTER PLAY",
		"SHA256:testfingerprint",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if strings.Contains(view, "Grow.") {
		t.Error("offline game should not show its tagline")
	}
}

func TestViewTooSmallSuspendsHitboxes(t *testing.T) {
	f := newFixture(t, twoGames())
	f.update(t, tea.WindowSizeMsg{Width: 60, Height: 20})
	view := renderPlain(f.model)
	if !strings.Contains(view, "RESIZE TERMINAL") {
		t.Fatal("no resize prompt below 80×24")
	}
	if len(f.model.hits.boxes) != 0 {
		t.Fatal("hitboxes registered on the too-small screen")
	}
}

func TestViewCentersOnLargerTerminals(t *testing.T) {
	f := newFixture(t, twoGames())
	f.update(t, tea.WindowSizeMsg{Width: 120, Height: 40})
	_ = f.model.View()
	// The first game's hitbox must shift by the centering offsets.
	box, ok := f.model.hits.At((120-80)/2+1, (40-24)/2+1+gamesTopRow)
	if !ok || box.ID != "game:moon" {
		t.Fatalf("hitbox not at centered coords: %+v ok=%v", box, ok)
	}
}

func TestBorderLinesAreFrameWidth(t *testing.T) {
	f := newFixture(t, twoGames())
	m := f.model
	if w := lipgloss.Width(m.topBorder()); w != frameW {
		t.Fatalf("top border width = %d, want %d", w, frameW)
	}
	if w := lipgloss.Width(m.bottomBorder()); w != frameW {
		t.Fatalf("bottom border width = %d, want %d", w, frameW)
	}
	m.blink = true
	if w := lipgloss.Width(m.bottomBorder()); w != frameW {
		t.Fatalf("bottom border width with cursor = %d, want %d", w, frameW)
	}
}

// renderPlain renders the view and strips ANSI sequences.
func renderPlain(m Model) string {
	return stripANSI(m.View().Content)
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\x1b' {
			b.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[':
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			i++
		case ']':
			i++
			for i < len(s) && s[i] != '\a' {
				if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
			i++
		default:
			i++
		}
	}
	return b.String()
}

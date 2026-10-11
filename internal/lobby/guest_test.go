package lobby

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/banner"
	"strings"
	"testing"
)

func TestGuestPolicyAllInputPaths(t *testing.T) {
	for _, input := range []string{"enter", "space", "click", "double-click"} {
		t.Run(input, func(t *testing.T) {
			src := twoGames()
			src.games[0].Version = "1.0.0"
			f := newFixture(t, src, WithGuest())
			_ = f.model.View()
			var cmd tea.Cmd
			if strings.Contains(input, "click") {
				box := f.model.hits.boxes[0]
				cmd = f.update(t, tea.MouseClickMsg{X: box.X, Y: box.Y, Button: tea.MouseLeft})
				if input == "double-click" {
					cmd = f.update(t, tea.MouseClickMsg{X: box.X, Y: box.Y, Button: tea.MouseLeft})
				}
			} else {
				cmd = f.press(t, input)
			}
			if isQuit(cmd) || f.model.alphaOpen || f.model.Result().Kind != KindNone || !strings.Contains(f.model.flash, "KEY REQUIRED") {
				t.Fatalf("guest crossed gate: result=%+v flash=%q alpha=%v", f.model.Result(), f.model.flash, f.model.alphaOpen)
			}
		})
	}
}

func TestAlphaRevalidatesCurrentRegistryBeforeAcknowledging(t *testing.T) {
	for _, change := range []string{"removed", "offline", "key-required", "address"} {
		t.Run(change, func(t *testing.T) {
			src := twoGames()
			src.games[0].AllowGuests = true
			src.games[0].Version = "1.0.0"
			prefs := &fakePrefs{}
			f := newFixture(t, src, WithGuest())
			f.model.prefs = prefs
			f.press(t, "enter")
			f.press(t, "space")
			switch change {
			case "removed":
				src.games = nil
			case "offline":
				src.games[0].Online = false
			case "key-required":
				src.games[0].AllowGuests = false
			case "address":
				src.games[0].Addr = "127.0.0.1:9999"
			}
			cmd := f.press(t, "enter")
			if change == "address" {
				if !isQuit(cmd) || f.model.Result().Game.Addr != "127.0.0.1:9999" {
					t.Fatal("launched stale address")
				}
			} else if isQuit(cmd) || len(prefs.acks) != 0 {
				t.Fatal("launched or persisted acknowledgment after access changed")
			}
		})
	}
}

func TestGuestChromeFitsAndKeepsPolicyVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {108, 32}} {
		src := twoGames()
		src.games[0].AllowGuests = true
		src.games[0].Descriptors = []string{strings.Repeat("long", 40)}
		f := newFixture(t, src, WithGuest())
		f.model.width, f.model.height = size[0], size[1]
		f.model.notice = banner.Notice{Enabled: true, Title: "NOTICE", Message: "hello"}
		v := f.model.View().Content
		for _, want := range []string{"Guest Account", "Guest progress won’t be saved", "ssh-keygen -t ed25519", "GUEST OK", "KEY REQUIRED"} {
			if !strings.Contains(v, want) {
				t.Fatalf("missing %q", want)
			}
		}
		if strings.Contains(v, "SHA256:") || strings.Contains(v, "Your key is your account") {
			t.Fatal("keyed chrome leaked")
		}
		for _, line := range strings.Split(v, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatal("overflow")
			}
		}
	}
}

func TestKeyedAlphaUsesCurrentPolicyToo(t *testing.T) {
	src := twoGames()
	src.games[0].Version = "1.0.0"
	f := newFixture(t, src)
	f.press(t, "enter")
	src.games = nil
	if isQuit(f.press(t, "enter")) {
		t.Fatal("removed game launched")
	}
}

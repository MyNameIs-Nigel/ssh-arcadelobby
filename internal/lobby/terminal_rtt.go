package lobby

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The terminal answers this VT query through the same PTY as the player,
// including the public lobby bridge. Existing UI heartbeats drive sampling.
type terminalRTT struct {
	sent  time.Time
	next  time.Time
	label string
}

const probeInterval = 5 * time.Second

func (r *terminalRTT) observe(visible bool, msg tea.Msg, now time.Time) tea.Cmd {
	if _, reply := msg.(tea.CursorPositionMsg); reply && !r.sent.IsZero() {
		elapsed := now.Sub(r.sent)
		r.sent = time.Time{}
		r.next = now.Add(probeInterval)
		if elapsed < probeInterval {
			r.label = fmt.Sprintf("%d ms", elapsed.Round(time.Millisecond).Milliseconds())
		} else {
			r.label = "unavailable"
		}
	}
	if !visible {
		r.label = ""
		r.next = time.Time{}
		return nil
	}
	if !r.sent.IsZero() {
		// Cursor reports carry no request ID. Never overlap requests, even
		// after a timeout: a delayed reply must not become a fast new sample.
		if now.Sub(r.sent) >= probeInterval {
			r.label = "unavailable"
		}
		return nil
	}
	if now.Before(r.next) {
		return nil
	}
	r.sent = now
	return tea.RequestCursorPosition
}

func (r terminalRTT) String() string {
	label := r.label
	if label == "" {
		label = "measuring…"
	}
	return "Terminal RTT: " + label
}

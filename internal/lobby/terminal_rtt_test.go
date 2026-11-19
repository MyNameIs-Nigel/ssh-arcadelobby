package lobby

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestTerminalRTT(t *testing.T) {
	var r terminalRTT
	start := time.Unix(100, 0)
	if r.observe(false, nil, start) != nil {
		t.Fatal("probe outside help")
	}
	if r.observe(true, nil, start) == nil {
		t.Fatal("no probe on opening help")
	}
	if r.observe(true, nil, start.Add(time.Millisecond)) != nil {
		t.Fatal("overlapping probe")
	}
	r.observe(true, tea.CursorPositionMsg{}, start.Add(150*time.Millisecond))
	if got := r.String(); got != "Terminal RTT: 150 ms" {
		t.Fatal(got)
	}
	next := start.Add(6 * time.Second)
	if r.observe(true, nil, next) == nil {
		t.Fatal("no second sample")
	}
	if r.observe(true, nil, next.Add(probeInterval)) != nil || r.String() != "Terminal RTT: unavailable" {
		t.Fatal("timeout", r.String())
	}
	r.observe(false, tea.CursorPositionMsg{}, next.Add(2*probeInterval))
	if r.observe(true, nil, next.Add(3*probeInterval)) == nil {
		t.Fatal("could not resample after late reply")
	}
}

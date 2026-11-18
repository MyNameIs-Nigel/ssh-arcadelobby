package server

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type probeModel struct{}

func (probeModel) Init() tea.Cmd                         { return tea.Quit }
func (m probeModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (probeModel) View() tea.View                        { return tea.NewView("ready") }

func TestSSHSessionProbeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, term string
		env        []string
		want       bool
	}{
		{"generic", "xterm-256color", nil, false},
		{"apple", "xterm-256color", []string{"TERM_PROGRAM=Apple_Terminal"}, false},
		{"recognized terminal", "xterm-kitty", nil, true},
		{"windows terminal", "xterm-256color", []string{"WT_SESSION=known"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			env := teaSessionEnvironment(tc.env, tc.term)
			p := tea.NewProgram(probeModel{}, tea.WithEnvironment(env), tea.WithInput(strings.NewReader("")), tea.WithOutput(&output), tea.WithWindowSize(80, 24))
			if _, err := p.Run(); err != nil {
				t.Fatal(err)
			}
			for _, seq := range []string{"\x1b[?2026$p", "\x1b[?2027$p"} {
				if got := strings.Contains(output.String(), seq); got != tc.want {
					t.Errorf("probe %q present = %v, want %v; output %q", seq, got, tc.want, output.String())
				}
			}
		})
	}
}

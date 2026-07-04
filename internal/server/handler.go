package server

import (
	"errors"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/bridge"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/lobby"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/proxyproto"
)

// terminalReset is written between a game exiting and the lobby restarting.
// A game may leave mouse tracking, the alt screen, a hidden cursor, or SGR
// attributes enabled; without a conservative reset the second lobby visit
// renders garbage. Disables all mouse-tracking modes (1000/1002/1003 + SGR
// 1006 encoding), leaves the alt screen, shows the cursor, resets SGR.
const terminalReset = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l\x1b[?25h\x1b[0m"

const (
	goodbyeMessage = "\r\nThanks for playing — same key, same saves, any time.\r\n"
	idleMessage    = "\r\nLobby idle — disconnecting to free the seat. Come back any time!\r\n"
)

// handle is the session handler: alternate the lobby menu and bridged game
// sessions on one terminal until the player leaves. Custom (not the wish
// bubbletea middleware) because the stock middleware owns the session for
// its whole life.
func (srv *Server) handle(s ssh.Session) {
	fp := proxyproto.Fingerprint(s.PublicKey())
	logger := srv.logger.With(
		"fingerprint", truncate(fp, 16),
		"remote", s.RemoteAddr().String(),
	)

	pump := newInputPump(s)
	_, winCh, _ := s.Pty()
	wp := newWinchPump(winCh)

	logger.Info("lobby session start")
	start := time.Now()
	defer func() {
		logger.Info("lobby session end", "duration", time.Since(start).Round(time.Second))
	}()

	var flash string
	for {
		res := srv.runLobby(s, pump, wp, fp, flash)
		flash = ""

		switch res.Kind {
		case lobby.KindQuit:
			_, _ = io.WriteString(s, goodbyeMessage)
			return
		case lobby.KindIdle:
			_, _ = io.WriteString(s, idleMessage)
			return
		case lobby.KindNone:
			// Disconnected or program failure; nothing to say.
			return
		case lobby.KindPlay:
		}

		game := res.Game
		bridgeStart := time.Now()

		// The bridge reads player input from a pump-fed pipe (see pump.go)
		// and resizes from the winch pump.
		pr, pw := io.Pipe()
		pump.SetDest(pw)
		err := bridge.Run(s, game, srv.proxyKey, bridge.Options{
			Input:  pr,
			Winch:  wp.Attach(),
			Logger: srv.logger,
		})
		wp.Detach()
		pump.SetDest(nil)
		// Unblock the bridge's stdin copier and any in-flight pump write.
		_ = pw.Close()
		_ = pr.Close()

		_, _ = io.WriteString(s, terminalReset)
		logger.Info("bridge session",
			"game", game.ID,
			"duration", time.Since(bridgeStart).Round(time.Millisecond),
			"clean", err == nil,
		)

		var exitErr *gossh.ExitError
		switch {
		case err == nil:
			// Clean game exit: back to the menu silently.
		case errors.Is(err, bridge.ErrDialFailed):
			// A game that died between probes flips Offline immediately.
			srv.registry.Report(game.ID, err)
			flash = strings.ToUpper(game.Name) + " IS OFFLINE — TRY LATER"
		case errors.As(err, &exitErr):
			flash = strings.ToUpper(game.Name) + " CLOSED THE SESSION"
		default:
			flash = "CONNECTION TO " + strings.ToUpper(game.Name) + " LOST — PROGRESS SAVED"
		}

		select {
		case <-s.Context().Done():
			return
		case <-pump.Done():
			return
		default:
		}
	}
}

// runLobby runs one menu program over the session's PTY and returns how it
// ended. Mirrors what wish's bubbletea middleware does internally
// (bubbletea.MakeOptions), plus the Windows-host PTY fixes, with input
// coming from the pump instead of the raw session.
func (srv *Server) runLobby(s ssh.Session, pump *inputPump, wp *winchPump, fp, flash string) lobby.Result {
	pty, _, _ := s.Pty()

	pr, pw := io.Pipe()
	pump.SetDest(pw)
	defer func() {
		pump.SetDest(nil)
		// Closing both ends unblocks the program's abandoned input read and
		// any in-flight pump write.
		_ = pw.Close()
		_ = pr.Close()
	}()

	poke := srv.registry.Subscribe()
	defer srv.registry.Unsubscribe(poke)

	opts := []lobby.Option{}
	if flash != "" {
		opts = append(opts, lobby.WithFlash(flash))
	}
	model := lobby.New(srv.registry, poke, fp,
		pty.Window.Width, pty.Window.Height, srv.cfg.LobbyIdleTimeout, opts...)

	envs := append(s.Environ(), "TERM="+pty.Term)
	progOpts := []tea.ProgramOption{
		tea.WithInput(pr),
		tea.WithOutput(s),
		// Emulated PTY: force the color profile from the session env, as
		// wish's MakeOptions does on unix hosts.
		tea.WithColorProfile(colorprofile.Env(envs)),
		tea.WithEnvironment(envs),
		tea.WithWindowSize(pty.Window.Width, pty.Window.Height),
		tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if _, ok := msg.(tea.SuspendMsg); ok {
				return tea.ResumeMsg{}
			}
			return msg
		}),
	}
	progOpts = append(progOpts, windowsPtyOptions(s)...)

	p := tea.NewProgram(model, progOpts...)

	// Forward resizes and quit the program if the connection dies.
	wch := wp.Attach()
	stop := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		for {
			select {
			case <-stop:
				return
			case <-s.Context().Done():
				p.Quit()
				return
			case <-pump.Done():
				p.Quit()
				return
			case w, ok := <-wch:
				if !ok {
					p.Quit()
					return
				}
				p.Send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
			}
		}
	}()

	final, err := p.Run()
	wp.Detach()
	close(stop)
	<-watcherDone

	if err != nil {
		srv.logger.Warn("lobby program error", "error", err)
		return lobby.Result{}
	}
	m, ok := final.(lobby.Model)
	if !ok {
		return lobby.Result{}
	}
	return m.Result()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

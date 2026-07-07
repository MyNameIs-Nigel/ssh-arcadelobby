// Package lobby is the arcade menu: the Bubble Tea model a player lands in,
// with live online/offline status from the registry, keyboard and mouse
// input, and a Result telling the session handler what to do next.
package lobby

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/banner"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
)

// Source is the registry surface the menu consumes (doc 03).
type Source interface {
	Games() []registry.GameStatus
	ProbeNow()
}

// BannerReader supplies the operator notice strip.
type BannerReader interface {
	Notice() banner.Notice
}

// AlphaPrefs answers whether this player dismissed an alpha warning.
type AlphaPrefs interface {
	HasAlphaAck(gameID string) bool
	AckAlpha(gameID string) error
}

// ResultKind says how the lobby program ended.
type ResultKind int

const (
	// KindNone: the program was quit externally (disconnect, shutdown).
	KindNone ResultKind = iota
	// KindPlay: bridge into Result.Game.
	KindPlay
	// KindQuit: the player chose to leave.
	KindQuit
	// KindIdle: the lobby idle timeout fired.
	KindIdle
)

// Result is read by the session handler after the program returns.
type Result struct {
	Kind ResultKind
	Game registry.Game
}

// doubleClickWindow is how close two clicks on the same target must be to
// count as a double click.
const doubleClickWindow = 400 * time.Millisecond

// flashDuration is how long a flash notice stays on screen.
const flashDuration = 3 * time.Second

type (
	tickMsg time.Time
	pokeMsg struct{}
)

// Model is the arcade menu.
type Model struct {
	src         Source
	bannerSrc   BannerReader
	poke        <-chan struct{}
	bannerPoke  <-chan struct{}
	fingerprint string
	prefs       AlphaPrefs

	games  []registry.GameStatus
	notice banner.Notice
	cursor int

	width, height int
	aboutOpen     bool
	alphaOpen     bool
	alphaGame     registry.GameStatus
	alphaDontShow bool
	blink         bool

	flash      string
	flashUntil time.Time

	idleTimeout time.Duration
	lastInput   time.Time

	lastClickID string
	lastClickAt time.Time

	clock func() time.Time
	hits  *Hitboxes

	result Result
}

// Option customizes New.
type Option func(*Model)

// WithFlash shows a notice when the menu opens (offline game, lost bridge).
func WithFlash(text string) Option {
	return func(m *Model) {
		if text == "" {
			return
		}
		m.flash = text
		m.flashUntil = m.clock().Add(flashDuration)
	}
}

// WithClock injects a time source for tests.
func WithClock(clock func() time.Time) Option {
	return func(m *Model) {
		m.clock = clock
		m.lastInput = clock()
		if m.flash != "" {
			m.flashUntil = clock().Add(flashDuration)
		}
	}
}

// New builds the menu. poke and bannerPoke are subscribe channels; either may
// be nil.
func New(src Source, bannerSrc BannerReader, poke, bannerPoke <-chan struct{}, fingerprint string, prefs AlphaPrefs, width, height int, idleTimeout time.Duration, opts ...Option) Model {
	m := Model{
		src:         src,
		bannerSrc:   bannerSrc,
		poke:        poke,
		bannerPoke:  bannerPoke,
		fingerprint: fingerprint,
		prefs:       prefs,
		width:       width,
		height:      height,
		idleTimeout: idleTimeout,
		clock:       time.Now,
		hits:        &Hitboxes{},
	}
	m.lastInput = m.clock()
	m.refreshGames()
	m.refreshBanner()
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Result reports how the program ended; valid after tea.Program.Run returns.
func (m Model) Result() Result { return m.result }

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), pokeCmd(m.poke), pokeCmd(m.bannerPoke))
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// pokeCmd waits for one notification. Re-armed on receipt; returns nil when
// the channel closes (unsubscribed) so it never spins.
func pokeCmd(ch <-chan struct{}) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return pokeMsg{}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case pokeMsg:
		m.refreshGames()
		m.refreshBanner()
		return m, tea.Batch(pokeCmd(m.poke), pokeCmd(m.bannerPoke))

	case tickMsg:
		now := m.clock()
		m.blink = !m.blink
		if m.flash != "" && now.After(m.flashUntil) {
			m.flash = ""
		}
		m.refreshGames()
		m.refreshBanner()
		if m.idleTimeout > 0 && now.Sub(m.lastInput) >= m.idleTimeout {
			m.result = Result{Kind: KindIdle}
			return m, tea.Quit
		}
		return m, tickCmd()

	case tea.KeyPressMsg:
		m.lastInput = m.clock()
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		m.lastInput = m.clock()
		return m.handleClick(tea.Mouse(msg))

	case tea.MouseWheelMsg:
		m.lastInput = m.clock()
		return m.handleWheel(tea.Mouse(msg))
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		m.result = Result{Kind: KindQuit}
		return m, tea.Quit
	}
	if m.alphaOpen {
		switch key {
		case "space", " ":
			m.alphaDontShow = !m.alphaDontShow
		case "enter":
			return m.confirmAlpha()
		case "esc", "backspace":
			m.alphaOpen = false
		}
		return m, nil
	}
	if m.aboutOpen {
		m.aboutOpen = false
		return m, nil
	}

	switch key {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "enter", "space", " ":
		return m.activate()
	case "r":
		m.src.ProbeNow()
		m.setFlash("RE-CHECKING GAMES…")
	case "?":
		m.aboutOpen = true
	case "q":
		m.result = Result{Kind: KindQuit}
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	if m.alphaOpen {
		box, ok := m.hits.At(mouse.X, mouse.Y)
		if !ok {
			return m, nil
		}
		switch box.ID {
		case "alpha:check":
			m.alphaDontShow = !m.alphaDontShow
		case "alpha:continue":
			return m.confirmAlpha()
		}
		return m, nil
	}
	if m.aboutOpen {
		m.aboutOpen = false
		return m, nil
	}
	box, ok := m.hits.At(mouse.X, mouse.Y)
	if !ok {
		return m, nil
	}
	idx, ok := box.Data.(int)
	if !ok || idx < 0 || idx >= len(m.games) {
		return m, nil
	}

	now := m.clock()
	isDouble := box.ID == m.lastClickID && now.Sub(m.lastClickAt) <= doubleClickWindow
	m.lastClickID = box.ID
	m.lastClickAt = now

	if idx == m.cursor || isDouble {
		m.cursor = idx
		return m.activate()
	}
	m.cursor = idx
	return m, nil
}

func (m Model) handleWheel(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	if m.alphaOpen || m.aboutOpen {
		return m, nil
	}
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.moveCursor(-1)
	case tea.MouseWheelDown:
		m.moveCursor(1)
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.games) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.games) {
		m.cursor = len(m.games) - 1
	}
}

// activate launches the selected game — or flashes when it is offline
// (offline rows stay selectable, Enter just refuses politely).
func (m Model) activate() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.games) {
		return m, nil
	}
	g := m.games[m.cursor]
	if !g.Online {
		m.setFlash("GAME OFFLINE — TRY LATER")
		return m, nil
	}
	if m.needsAlphaWarning(g) {
		m.alphaOpen = true
		m.alphaGame = g
		m.alphaDontShow = false
		return m, nil
	}
	return m.launchGame(g.Game)
}

func (m Model) needsAlphaWarning(g registry.GameStatus) bool {
	v := g.DetectedVersion
	if v == "" {
		v = g.Version
	}
	if registry.Channel(v) != "alpha" {
		return false
	}
	if m.prefs == nil {
		return true
	}
	return !m.prefs.HasAlphaAck(g.ID)
}

func (m Model) confirmAlpha() (tea.Model, tea.Cmd) {
	g := m.alphaGame
	if m.alphaDontShow && m.prefs != nil {
		_ = m.prefs.AckAlpha(g.ID)
	}
	m.alphaOpen = false
	return m.launchGame(g.Game)
}

func (m Model) launchGame(g registry.Game) (tea.Model, tea.Cmd) {
	m.result = Result{Kind: KindPlay, Game: g}
	return m, tea.Quit
}

func (m *Model) setFlash(text string) {
	m.flash = text
	m.flashUntil = m.clock().Add(flashDuration)
}

func (m *Model) refreshGames() {
	m.games = m.src.Games()
	if m.cursor >= len(m.games) {
		m.cursor = len(m.games) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) refreshBanner() {
	if m.bannerSrc == nil {
		m.notice = banner.Notice{}
		return
	}
	m.notice = m.bannerSrc.Notice()
}

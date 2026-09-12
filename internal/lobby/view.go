package lobby

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mynameis-nigel/ssh-arcadelobby/internal/banner"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/registry"
	"github.com/mynameis-nigel/ssh-arcadelobby/internal/version"
)

const (
	frameW      = 80
	frameH      = 24
	innerW      = frameW - 2 // content columns between the │ borders
	contentRows = frameH - 2

	hostLabel   = "ssharcade.dev"
	windowTitle = "SSHARCADE"

	// Layout slots inside the content region (see doc 01's mock).
	gamesTopRow       = 1 // first game name line when no banner
	maxVisible        = 7 // game rows that fit (2 lines each)
	bannerTopRow      = 0 // operator notice when enabled
	bannerReserveRows = 4 // blank rows reserved above the game list
	identityRow       = 16
	flashRow          = 18
	fingerRow         = 20
	nameColWidth      = 14
	rowIndent         = 3
)

// View renders the 80×24 menu, centered when the terminal is larger, and
// rebuilds the hitbox registry to match what is on screen.
func (m Model) View() tea.View {
	m.hits.Reset()

	var body string
	if m.width < frameW || m.height < frameH {
		body = m.viewTooSmall()
	} else {
		body = m.viewFrame()
	}
	body = styleScreen.Width(max(m.width, 1)).Height(max(m.height, 1)).Render(body)

	v := tea.NewView(body)
	v.AltScreen = true
	v.WindowTitle = windowTitle
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) viewTooSmall() string {
	msg := fmt.Sprintf("RESIZE TERMINAL — need %d×%d, have %d×%d", frameW, frameH, m.width, m.height)
	return lipgloss.Place(max(m.width, 1), max(m.height, 1),
		lipgloss.Center, lipgloss.Center, styleFlash.Render(msg))
}

func (m Model) viewFrame() string {
	ox := (m.width - frameW) / 2
	oy := (m.height - frameH) / 2

	content := make([]string, contentRows)
	switch {
	case m.alphaOpen:
		m.fillAlphaWarn(content, ox, oy)
	case m.aboutOpen:
		m.fillAbout(content)
	default:
		m.fillMenu(content, ox, oy)
	}

	var b strings.Builder
	b.WriteString(strings.Repeat("\n", oy))
	pad := strings.Repeat(" ", ox)

	b.WriteString(pad + m.topBorder() + "\n")
	for _, line := range content {
		b.WriteString(pad + styleFrame.Render("│") + padLine(line) + styleFrame.Render("│") + "\n")
	}
	b.WriteString(pad + m.bottomBorder())
	return b.String()
}

func (m Model) topBorder() string {
	left := "┌◇ "
	right := " ┐"
	title := "SSHARCADE"
	fill := frameW - lipgloss.Width(left) - len(title) - 2 - len(hostLabel) - lipgloss.Width(right)
	return styleFrame.Render(left) + styleTitle.Render(title) + styleFrame.Render(" "+strings.Repeat("─", fill)+" ") +
		styleHost.Render(hostLabel) + styleFrame.Render(right)
}

func (m Model) bottomBorder() string {
	keybar := "↑↓ SELECT · ENTER PLAY · R REFRESH · ? ABOUT · Q QUIT"
	cursor := " "
	if m.blink {
		cursor = "█"
	}
	left := "└ "
	right := " ┘"
	const dashPad = 2
	fill := frameW - lipgloss.Width(left) - lipgloss.Width(keybar) - lipgloss.Width(right) - lipgloss.Width(cursor) - dashPad
	return styleFrame.Render(left) + styleKeybar.Render(keybar) + styleFrame.Render(" "+strings.Repeat("─", fill)+" ") +
		styleBlink.Render(cursor) + styleFrame.Render(right)
}

func (m Model) gamesOrigin() int {
	if m.notice.Enabled {
		return gamesTopRow + bannerReserveRows
	}
	return gamesTopRow
}

func (m Model) maxVisibleGames() int {
	if !m.notice.Enabled {
		return maxVisible
	}
	n := (identityRow - m.gamesOrigin()) / 2
	if n < 1 {
		return 1
	}
	if n > maxVisible {
		return maxVisible
	}
	return n
}

func (m Model) fillBanner(content []string) {
	if !m.notice.Enabled {
		return
	}
	bodyStyle := styleBannerInfo
	switch m.notice.Level {
	case banner.LevelWarning:
		bodyStyle = styleBannerWarn
	case banner.LevelDanger:
		bodyStyle = styleBannerDanger
	}
	if m.notice.Title != "" {
		line := styleBannerTitle.Render(fit(m.notice.Title, innerW-rowIndent))
		content[bannerTopRow] = strings.Repeat(" ", rowIndent) + line
	}
	if m.notice.Message != "" {
		line := bodyStyle.Render(fit(m.notice.Message, innerW-rowIndent))
		row := bannerTopRow
		if m.notice.Title != "" {
			row++
		}
		if row < identityRow-1 {
			content[row] = strings.Repeat(" ", rowIndent) + line
		}
	}
}

// fillMenu writes the game list and chrome into the content rows and
// registers a hitbox per visible game row.
func (m Model) fillMenu(content []string, ox, oy int) {
	m.fillBanner(content)

	origin := m.gamesOrigin()
	limit := m.maxVisibleGames()
	first := m.scrollOffset(limit)
	visible := m.games[first:min(first+limit, len(m.games))]

	for vi, g := range visible {
		idx := first + vi
		selected := idx == m.cursor

		cursor := "  "
		if selected {
			cursor = styleCursor.Render("▸ ")
		}
		dot := styleOffline.Render("○ ")
		if g.Online {
			dot = styleOnline.Render("● ")
		}

		nameStyle := styleName
		switch {
		case !g.Online:
			nameStyle = styleNameOff
		case selected:
			nameStyle = styleNameSel
		}
		name := nameStyle.Render(fit(g.Name, nameColWidth))

		tagline := g.Tagline
		tagStyle := styleTagline
		if !g.Online {
			tagline = "OFFLINE — back soon"
			tagStyle = styleOffline
		}
		tagWidth := innerW - rowIndent - 2 - 2 - nameColWidth - 1
		nameLine := strings.Repeat(" ", rowIndent) + cursor + dot + name + " " + tagStyle.Render(fit(tagline, tagWidth))

		desc := strings.Join(g.Descriptors, " · ")
		gameVersion := g.DetectedVersion
		if gameVersion == "" {
			gameVersion = g.Version
		}
		if v := versionLabel(gameVersion); v != "" {
			if desc == "" {
				desc = v
			} else {
				desc += " · " + v
			}
		}
		descStyle := styleDesc
		if !g.Online {
			descStyle = styleOffline
		}
		descLine := strings.Repeat(" ", rowIndent+4) + descStyle.Render(fit(desc, innerW-rowIndent-4))

		row := origin + vi*2
		if row+1 >= identityRow-1 {
			break
		}
		content[row] = nameLine
		content[row+1] = descLine

		m.hits.Add(Box{
			X: ox + 1, Y: oy + 1 + row, W: innerW, H: 2,
			ID:   "game:" + g.ID,
			Data: idx,
		})
	}

	content[identityRow] = strings.Repeat(" ", rowIndent) +
		styleIdentity.Render("Your key is your account. Same key, same saves, any game.")

	if m.flash != "" {
		f := styleFlash.Render(m.flash)
		content[flashRow] = strings.Repeat(" ", max((innerW-lipgloss.Width(f))/2, 0)) + f
	}

	if m.fingerprint != "" {
		fp := styleFinger.Render("key " + fit(m.fingerprint, 24))
		content[fingerRow] = strings.Repeat(" ", max(innerW-lipgloss.Width(fp)-2, 0)) + fp
	}
}

// versionLabel formats a registry version for the menu: "v2.0.0 beta".
func versionLabel(v string) string {
	if v == "" {
		return ""
	}
	label := "v" + v
	if ch := registry.Channel(v); ch != "" {
		label += " " + ch
	}
	return label
}

// scrollOffset keeps the cursor visible when more games exist than fit.
func (m Model) scrollOffset(limit int) int {
	if len(m.games) <= limit {
		return 0
	}
	first := m.cursor - limit + 1
	if first < 0 {
		first = 0
	}
	if first > len(m.games)-limit {
		first = len(m.games) - limit
	}
	return first
}

var aboutLines = []string{
	"",
	"ABOUT SSHARCADE",
	"router v" + version.Version + " (" + version.Channel + ")",
	"",
	"One address, every game:  ssh " + hostLabel,
	"",
	"Your SSH key is your account. When you enter a game, the arcade",
	"forwards a fingerprint of your public key over a trusted private",
	"network — so the same key opens the same saves in every game,",
	"whether you connect through the arcade or straight to a game.",
	"",
	"Your private key never leaves your machine; SSH proves who you",
	"are without sending it anywhere.",
	"",
	"Pick a save: the SSH username selects a save slot, e.g.",
	"  ssh scout@" + hostLabel,
	"",
	"",
	"PRESS ANY KEY TO RETURN",
}

func (m Model) fillAbout(content []string) {
	for i, line := range aboutLines {
		if i >= len(content) {
			break
		}
		style := styleAbout
		if strings.HasPrefix(line, "ABOUT") || strings.HasPrefix(line, "PRESS") {
			style = styleAboutH
		}
		content[i] = strings.Repeat(" ", rowIndent) + style.Render(fit(line, innerW-rowIndent))
	}
}

func (m Model) fillAlphaWarn(content []string, ox, oy int) {
	g := m.alphaGame
	lines := []string{
		"",
		"ALPHA SOFTWARE",
		"",
		fit(g.Name+" is in active development.", innerW-rowIndent),
		"Features, balance, and saves may change without notice.",
		"",
		m.alphaCheckboxLine(),
		"",
		"▸ CONTINUE TO GAME",
		"",
		"SPACE TOGGLE · ENTER CONTINUE · ESC BACK",
	}
	for i, line := range lines {
		if i >= len(content) {
			break
		}
		style := styleAbout
		switch {
		case strings.HasPrefix(line, "ALPHA"):
			style = styleAboutH
		case strings.HasPrefix(line, "▸ CONTINUE"):
			style = styleAlphaContinue
		case strings.HasPrefix(line, "SPACE"):
			style = styleAboutH
		case line == m.alphaCheckboxLine():
			style = styleAlphaCheck
		}
		content[i] = strings.Repeat(" ", rowIndent) + style.Render(fit(line, innerW-rowIndent))
	}

	checkRow := 6
	continueRow := 8
	m.hits.Add(Box{
		X: ox + 1, Y: oy + 1 + checkRow, W: innerW, H: 1,
		ID: "alpha:check",
	})
	m.hits.Add(Box{
		X: ox + 1, Y: oy + 1 + continueRow, W: innerW, H: 1,
		ID: "alpha:continue",
	})
}

func (m Model) alphaCheckboxLine() string {
	box := "[ ]"
	if m.alphaDontShow {
		box = "[x]"
	}
	return box + " Don't show this again for this game"
}

// padLine pads a styled line to the interior width, truncation-safe.
func padLine(line string) string {
	w := lipgloss.Width(line)
	if w > innerW {
		return line
	}
	return line + strings.Repeat(" ", innerW-w)
}

// fit truncates a plain string to width, ellipsizing when needed.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

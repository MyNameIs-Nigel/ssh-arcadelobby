package lobby

import "charm.land/lipgloss/v2"

// The arcade menu speaks the same phosphor-blue language as the games.
var (
	styleScreen        = lipgloss.NewStyle().Background(lipgloss.Color("234"))
	styleFrame         = lipgloss.NewStyle().Foreground(lipgloss.Color("24"))
	styleTitle         = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	styleHost          = lipgloss.NewStyle().Foreground(lipgloss.Color("31"))
	styleCursor        = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	styleOnline        = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	styleOffline       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleName          = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleNameSel       = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	styleNameOff       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleTagline       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleDesc          = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleIdentity      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleFlash         = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleKeybar        = lipgloss.NewStyle().Foreground(lipgloss.Color("31"))
	styleBlink         = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	styleFinger        = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleAboutH        = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
	styleAbout         = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	styleBannerTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Bold(true)
	styleBannerInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleBannerWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleBannerDanger  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	styleAlphaCheck    = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	styleAlphaContinue = lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true)
)

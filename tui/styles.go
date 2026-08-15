package tui

import "github.com/charmbracelet/lipgloss"

const idleMenuWidth = 56

var (
	colorBg      = lipgloss.Color("0")
	colorFg      = lipgloss.Color("7")
	colorDim     = lipgloss.Color("8")
	colorBright  = lipgloss.Color("15")
	colorInverse = lipgloss.Color("0")
)

var (
	styleApp = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBg)

	styleTitle = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg).
			Bold(true).
			Padding(0, 1)

	styleMenuItem = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBg).
			Padding(0, 1)

	styleMenuSelected = lipgloss.NewStyle().
				Foreground(colorInverse).
				Background(colorBright).
				Bold(true).
				Padding(0, 1)

	styleHint = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBg)

	styleHelpKey = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg)

	styleStatus = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg).
			Padding(0, 1)

	styleInput = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBg)

	stylePlaceholder = lipgloss.NewStyle().
				Foreground(colorDim).
				Background(colorBg)

	stylePrompt = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg)

	styleButton = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBg).
			Padding(0, 1)

	styleButtonFocused = lipgloss.NewStyle().
				Foreground(colorInverse).
				Background(colorBright).
				Bold(true).
				Padding(0, 1)

	styleBox = lipgloss.NewStyle().
			Foreground(colorFg).
			Background(colorBg).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorFg).
			BorderBackground(colorBg).
			Padding(0, 1)

	styleBoxEmpty = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBg).
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorDim).
			BorderBackground(colorBg)

	styleBoxLabel = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg).
			Bold(true)

	styleBoxMeta = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(colorBg)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorBright).
			Background(colorBg)
)

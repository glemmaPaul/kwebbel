package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	var body string
	switch m.screen {
	case screenCalling:
		body = m.viewCalling()
	default:
		body = m.viewIdle()
	}

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Left,
		lipgloss.Top,
		styleApp.Width(m.width).Height(m.height).Render(body),
		lipgloss.WithWhitespaceBackground(colorBg),
	)
}

func (m model) viewIdle() string {
	header := lipgloss.NewStyle().
		Background(colorBg).
		Padding(1, 1, 0, 1).
		Render(styleTitle.Render("KWEBBEL"))

	menu := lipgloss.JoinVertical(lipgloss.Left,
		m.viewConnectItem(),
		m.menuLine(m.idle.menuIndex == menuAddressBook, "Address Book", "Coming soon"),
		m.menuLine(m.idle.menuIndex == menuProfile, "Profile", "Coming soon"),
	)
	help := viewKeyHints()
	footer := viewFooter(m.width, help)

	status := ""
	statusH := 0
	if m.status != "" {
		status = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, styleStatus.Render(m.status),
			lipgloss.WithWhitespaceBackground(colorBg))
		statusH = lipgloss.Height(status)
	}

	centerH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - statusH
	if centerH < 1 {
		centerH = 1
	}

	middle := lipgloss.Place(m.width, centerH, lipgloss.Center, lipgloss.Center, menu,
		lipgloss.WithWhitespaceBackground(colorBg))

	parts := []string{header, middle}
	if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func keyHint(key, label string) string {
	return styleHelpKey.Render(key) + styleHint.Render(" "+label)
}

func viewKeyHints() string {
	gap := styleHint.Render("   ")
	return lipgloss.JoinHorizontal(lipgloss.Center,
		keyHint("Arrows", "move"),
		gap,
		keyHint("Enter", "select"),
		gap,
		keyHint("CTRL+C", "quit"),
	)
}

func viewFooter(width int, content string) string {
	return lipgloss.PlaceHorizontal(width, lipgloss.Left,
		lipgloss.NewStyle().Background(colorBg).Padding(0, 1).Render(content),
		lipgloss.WithWhitespaceBackground(colorBg))
}

func (m model) viewConnectItem() string {
	selected := m.idle.menuIndex == menuConnect
	header := m.menuLine(selected, "Connect to", "")

	input := menuRow(false, "    "+m.idle.peerID.View())
	btnStyle := styleButton
	if selected && m.idle.focus == idleFocusConnect {
		btnStyle = styleButtonFocused
	}
	button := menuRow(false, "    "+btnStyle.Render("[Connect]"))

	return lipgloss.JoinVertical(lipgloss.Left, header, input, button)
}

func (m model) menuLine(selected bool, label, hint string) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	line := prefix + label
	if hint != "" {
		line += "  " + styleHint.Render("("+hint+")")
	}
	return menuRow(selected, line)
}

func menuRow(selected bool, content string) string {
	st := styleMenuItem
	if selected {
		st = styleMenuSelected
	}
	return st.Width(idleMenuWidth).MaxWidth(idleMenuWidth).Render(content)
}

func (m model) viewCalling() string {
	header := lipgloss.NewStyle().
		Background(colorBg).
		Padding(1, 1, 0, 1).
		Render(styleTitle.Render("KWEBBEL — CALL"))

	footer := viewFooter(m.width, m.viewCallControls())

	statusH := 0
	status := ""
	if m.status != "" {
		status = styleStatus.Padding(0, 1).Render(m.status)
		statusH = lipgloss.Height(status)
	}

	gridH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - statusH
	if gridH < 6 {
		gridH = 6
	}
	grid := viewCallerGrid(m.calling.callers, m.width-2, gridH)

	parts := []string{header, grid}
	if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m model) viewCallControls() string {
	muteLabel := "mute"
	if m.calling.muted {
		muteLabel = "unmute"
	}
	gap := styleHint.Render("   ")
	return lipgloss.JoinHorizontal(lipgloss.Center,
		keyHint("M", muteLabel),
		gap,
		keyHint("SHIFT+Q", "disconnect"),
	)
}

func viewCallerGrid(callers []Caller, width, height int) string {
	n := len(callers)
	if n == 0 {
		empty := styleHint.Render("Waiting for callers...")
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, empty, lipgloss.WithWhitespaceBackground(colorBg))
	}
	if n > 6 {
		n = 6
		callers = callers[:6]
	}

	gap := 1
	switch n {
	case 1:
		return renderCallerBox(callers[0], width, height)
	case 2:
		cellW := (width - gap) / 2
		left := renderCallerBox(callers[0], cellW, height)
		right := renderCallerBox(callers[1], cellW, height)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
	case 3:
		cellW := (width - gap) / 2
		cellH := (height - gap) / 2
		top := lipgloss.JoinHorizontal(
			lipgloss.Top,
			renderCallerBox(callers[0], cellW, cellH),
			strings.Repeat(" ", gap),
			renderCallerBox(callers[1], cellW, cellH),
		)
		bottom := renderCallerBox(callers[2], cellW, cellH)
		centered := lipgloss.PlaceHorizontal(lipgloss.Width(top), lipgloss.Center, bottom, lipgloss.WithWhitespaceBackground(colorBg))
		return lipgloss.JoinVertical(lipgloss.Left, top, centered)
	case 4:
		return renderUniformGrid(callers, 2, 2, width, height, gap)
	default:
		return renderUniformGrid(callers, 2, 3, width, height, gap)
	}
}

func renderUniformGrid(callers []Caller, rows, cols, width, height, gap int) string {
	cellW := (width - gap*(cols-1)) / cols
	cellH := (height - gap*(rows-1)) / rows
	if cellW < 8 {
		cellW = 8
	}
	if cellH < 4 {
		cellH = 4
	}

	var lines []string
	idx := 0
	for r := 0; r < rows; r++ {
		var cells []string
		for c := 0; c < cols; c++ {
			if idx < len(callers) {
				cells = append(cells, renderCallerBox(callers[idx], cellW, cellH))
			} else {
				cells = append(cells, renderEmptyBox(cellW, cellH))
			}
			idx++
		}
		row := cells[0]
		for i := 1; i < len(cells); i++ {
			row = lipgloss.JoinHorizontal(lipgloss.Top, row, strings.Repeat(" ", gap), cells[i])
		}
		lines = append(lines, row)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderCallerBox(c Caller, width, height int) string {
	innerW := width - 4
	if innerW < 4 {
		innerW = 4
	}
	label := c.Label
	if label == "" {
		label = "Caller"
	}
	if c.Local {
		label += " (you)"
	}
	id := c.ID
	muted := ""
	if c.Muted {
		muted = styleMuted.Render("MUTED")
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		styleBoxLabel.Render(truncate(label, innerW)),
		styleBoxMeta.Render(truncate(id, innerW)),
		"",
		muted,
	)
	return styleBox.Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(body)
}

func renderEmptyBox(width, height int) string {
	return styleBoxEmpty.Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render("")
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	runes := []rune(s)
	if max == 1 {
		return "…"
	}
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/j4mes/ttywx/modes"
)

// Dark-friendly palette.
var (
	colText   = lipgloss.Color("#e5e7eb")
	colDim    = lipgloss.Color("#8b93a1")
	colFaint  = lipgloss.Color("#4b5563")
	colAccent = lipgloss.Color("#7dd3fc")
	colRain   = lipgloss.Color("#60a5fa")
	colSelBg  = lipgloss.Color("#1f2937")
	colBlack  = lipgloss.Color("#111111")

	modeColors = map[modes.Mode]lipgloss.Color{
		modes.Normal:     lipgloss.Color("#4ade80"),
		modes.Warning:    lipgloss.Color("#facc15"),
		modes.Heightened: lipgloss.Color("#fb923c"),
		modes.TakeCover:  lipgloss.Color("#ef4444"),
	}
)

var (
	sText   = lipgloss.NewStyle().Foreground(colText)
	sDim    = lipgloss.NewStyle().Foreground(colDim)
	sFaint  = lipgloss.NewStyle().Foreground(colFaint)
	sAccent = lipgloss.NewStyle().Foreground(colAccent)
	sBold   = lipgloss.NewStyle().Foreground(colText).Bold(true)
	sRain   = lipgloss.NewStyle().Foreground(colRain)
	sStale  = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true)
	sErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171"))
)

func modeFg(m modes.Mode) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(modeColors[m])
}

// badge renders a solid mode label, e.g. " WARNING ".
func badge(m modes.Mode, short bool) string {
	label := m.String()
	if short {
		label = m.Short()
	}
	return lipgloss.NewStyle().Background(modeColors[m]).Foreground(colBlack).Bold(true).Render(" " + label + " ")
}

// tempColor maps a Fahrenheit temperature to a cold→hot color.
func tempColor(f float64) lipgloss.Color {
	switch {
	case f < 10:
		return "#c4b5fd"
	case f < 25:
		return "#a5b4fc"
	case f < 40:
		return "#93c5fd"
	case f < 55:
		return "#67e8f9"
	case f < 70:
		return "#86efac"
	case f < 82:
		return "#fde047"
	case f < 95:
		return "#fb923c"
	case f < 108:
		return "#f87171"
	}
	return "#f43f5e"
}

type borderSet struct{ tl, tr, bl, br, h, v string }

var (
	roundBorder = borderSet{"╭", "╮", "╰", "╯", "─", "│"}
	heavyBorder = borderSet{"┏", "┓", "┗", "┛", "━", "┃"}
)

// box draws a btop-style panel of exactly w×h cells: the title is set into
// the top border (left), an optional label on the top right, and a footer in
// the bottom border. Body lines are truncated or padded to fit.
type box struct {
	title, right, footer string
	body                 []string
	color                lipgloss.Color
	heavy                bool
}

func (b box) render(w, h int) string {
	if w < 4 || h < 2 {
		return blank(w, h)
	}
	bs := roundBorder
	if b.heavy {
		bs = heavyBorder
	}
	bc := lipgloss.NewStyle().Foreground(b.color)
	if b.heavy {
		bc = bc.Bold(true)
	}
	inner := w - 2

	// Top border: ╭─ title ───── right ─╮
	var top strings.Builder
	top.WriteString(bc.Render(bs.tl + bs.h))
	used := 1
	if b.title != "" {
		t := ansi.Truncate(" "+b.title+" ", inner-2, "…")
		top.WriteString(t)
		used += lipgloss.Width(t)
	}
	right := ""
	if b.right != "" && used+lipgloss.Width(b.right)+4 <= inner {
		right = " " + b.right + " "
	}
	fill := inner - used - lipgloss.Width(right) - 1
	top.WriteString(bc.Render(strings.Repeat(bs.h, max(0, fill))))
	top.WriteString(right)
	top.WriteString(bc.Render(bs.h + bs.tr))

	lines := []string{top.String()}
	cw := inner - 2 // one space of padding each side
	for i := 0; i < h-2; i++ {
		s := ""
		if i < len(b.body) {
			s = ansi.Truncate(b.body[i], cw, "…")
		}
		pad := max(0, cw-lipgloss.Width(s))
		lines = append(lines, bc.Render(bs.v)+" "+s+strings.Repeat(" ", pad)+" "+bc.Render(bs.v))
	}

	// Bottom border with right-aligned footer.
	foot := ""
	if b.footer != "" && lipgloss.Width(b.footer)+4 <= inner {
		foot = " " + b.footer + " "
	}
	fill = inner - lipgloss.Width(foot) - 1
	lines = append(lines, bc.Render(bs.bl+strings.Repeat(bs.h, max(0, fill)))+foot+bc.Render(bs.h+bs.br))
	return strings.Join(lines, "\n")
}

func blank(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	row := strings.Repeat(" ", w)
	rows := make([]string, h)
	for i := range rows {
		rows[i] = row
	}
	return strings.Join(rows, "\n")
}

// wrap word-wraps s to width, returning lines.
func wrap(s string, width int) []string {
	if width < 1 {
		return nil
	}
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

// padRight pads (or truncates) s to exactly w cells.
func padRight(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// fitLines forces a rendered block to exactly h lines of exactly w cells.
func fitLines(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = padRight(lines[i], w)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines, "\n")
}

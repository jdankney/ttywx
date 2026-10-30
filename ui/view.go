package ui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jdankney/ttywx/api"
	"github.com/jdankney/ttywx/config"
	"github.com/jdankney/ttywx/modes"
	"github.com/jdankney/ttywx/units"
	"github.com/jdankney/ttywx/wx"
)

const (
	minWidth, minHeight = 60, 20
	wideLayout          = 100 // at or above this width the grid sits beside the large panel
	cellMinW            = 28
)

func (m *Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	W, H := m.width, m.height
	if W < minWidth || H < minHeight {
		msg := fmt.Sprintf("ttywx needs at least %d×%d\ncurrent: %d×%d", minWidth, minHeight, W, H)
		return lipgloss.Place(W, H, lipgloss.Center, lipgloss.Center, sDim.Render(msg))
	}

	header := m.viewHeader(W)
	tabs := m.viewTabs(W)
	footer := m.viewFooter(W)
	banner := m.viewBanner(W)
	used := 3 + lipgloss.Height(banner)
	if banner == "" {
		used = 3
	}

	var body string
	switch {
	case m.help:
		body = lipgloss.Place(W, H-used, lipgloss.Center, lipgloss.Center, m.viewHelp(W-4))
	case m.expanded:
		body = m.viewExpanded(W, H-used)
	default:
		body = m.viewDashboard(W, H-used)
	}

	parts := []string{header, tabs}
	if banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, body, footer)
	return fitLines(strings.Join(parts, "\n"), W, H)
}

// ── header / footer / banner ──────────────────────────────────────────────

func (m *Model) viewHeader(W int) string {
	mode, idx := wx.Overall(m.locs)
	left := lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(" ttywx ") + badge(mode, false)
	if idx >= 0 {
		cause := m.locs[idx].Cfg.Name
		if c := m.locs[idx].Cause; c != nil {
			cause += " · " + c.Event
		}
		left += " " + modeFg(mode).Bold(true).Render(cause)
	} else {
		left += " " + sDim.Render("all clear")
	}
	if m.demo {
		left += " " + lipgloss.NewStyle().Background(lipgloss.Color("#a855f7")).Foreground(colBlack).Bold(true).Render(" DEMO ")
	}

	right := sDim.Render("alerts ") + m.dataAge(func(l *wx.Location) time.Time { return l.AlertsUpdated }) +
		sFaint.Render(" │ ") + sDim.Render("wx ") + m.dataAge(func(l *wx.Location) time.Time { return l.WxUpdated }) +
		sFaint.Render(" │ ") + sBold.Render(m.now.Format(m.u.Layout("Mon Jan 2  {clocksec}"))) + " "

	gap := W - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = sBold.Render(m.now.Format(m.u.Layout("{clocksec}"))) + " "
		gap = max(1, W-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + strings.Repeat(" ", gap) + right
}

// viewTabs is the tab bar, with tab-specific context on the right.
func (m *Model) viewTabs(W int) string {
	var b strings.Builder
	b.WriteString(" ")
	for t := tab(0); t < numTabs; t++ {
		label := fmt.Sprintf(" %d %s ", t+1, t)
		if t == m.tab {
			b.WriteString(lipgloss.NewStyle().Background(colAccent).Foreground(colBlack).Bold(true).Render(label))
		} else {
			b.WriteString(sDim.Render(label))
		}
		b.WriteString(" ")
	}
	left := b.String()
	var right string
	switch m.tab {
	case tabMain:
		if m.focus != 0 {
			right = sDim.Render("showing ") + sText.Render(m.locs[m.focus].Cfg.Name) + sDim.Render(" · esc: back to ") + sText.Render(m.locs[0].Cfg.Name)
		}
	case tabMap:
		right = sDim.Render("layer ") + sAccent.Bold(true).Render(m.mapLayer.String()) + sDim.Render(" · v: change")
	}
	right += " "
	return left + strings.Repeat(" ", max(1, W-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

// dataAge shows the age of the oldest successful update across locations.
func (m *Model) dataAge(get func(*wx.Location) time.Time) string {
	var oldest time.Time
	for _, l := range m.locs {
		if errors.Is(l.PointErr, api.ErrNoCoverage) {
			continue // never loads; the footer explains
		}
		t := get(l)
		if t.IsZero() {
			return sDim.Render("loading…")
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	return sText.Render(ago(m.now, oldest))
}

func (m *Model) viewFooter(W int) string {
	keys := [][2]string{{"q", "quit"}, {"r", "refresh"}, {"1-3/tab", "tabs"}}
	switch {
	case m.pane == paneAlerts:
		keys = append(keys, [2]string{"↑↓", "select"}, [2]string{"enter", "details"}, [2]string{"esc", "back"})
	case m.tab == tabMain:
		keys = append(keys, [2]string{"↓/a", "alerts"})
	case m.tab == tabNearby:
		keys = append(keys, [2]string{"←↑↓→", "move"}, [2]string{"enter", "open"}, [2]string{"a", "alerts"})
	case m.tab == tabMap:
		keys = append(keys, [2]string{"←↑↓→", "select"}, [2]string{"enter", "open"}, [2]string{"v", "layer"})
	}
	keys = append(keys, [2]string{"?", "help"})
	if m.demo {
		keys = append(keys, [2]string{"m/M", "demo mode"})
	}
	var b strings.Builder
	b.WriteString(" ")
	for _, k := range keys {
		b.WriteString(sAccent.Render(k[0]) + " " + sDim.Render(k[1]) + "  ")
	}
	s := b.String()
	if st := m.statusLine(); st != "" {
		s += sErr.Render(st)
	} else if m.cfg.Contact == config.PlaceholderContact {
		s += sStale.Render("set `contact` in " + m.cfg.Path())
	}
	return padRight(s, W)
}

// viewBanner is the full-width TAKE COVER banner, empty when not needed.
func (m *Model) viewBanner(W int) string {
	var hits []*wx.Location
	for _, l := range m.locs {
		if l.Mode == modes.TakeCover {
			hits = append(hits, l)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	first := hits[0]
	bg := modeColors[modes.TakeCover]
	if m.flash {
		bg = lipgloss.Color("#7f1d1d")
	}
	st := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("#ffffff"))
	inner := W - 4

	title := "▲ TAKE COVER ▲  " + strings.ToUpper(first.Cfg.Name)
	if first.Cause != nil {
		title += " — " + strings.ToUpper(first.Cause.Event)
	}
	if len(hits) > 1 {
		var others []string
		for _, l := range hits[1:] {
			others = append(others, l.Cfg.Name)
		}
		title += "   (also: " + strings.Join(others, ", ") + ")"
	}
	lines := []string{"", st.Bold(true).Render(padRight(title, inner))}
	if c := first.Cause; c != nil {
		if c.Headline != "" {
			lines = append(lines, st.Render(padRight(c.Headline, inner)))
		}
		instr := strings.Join(strings.Fields(c.Instruction), " ")
		wrapped := wrap(instr, inner)
		if len(wrapped) > 3 {
			wrapped = append(wrapped[:3], "… (select the alert below and press enter for full text)")
		}
		for _, w := range wrapped {
			lines = append(lines, st.Bold(true).Render(padRight(w, inner)))
		}
	}
	lines = append(lines, "")
	pad := st.Render("  ")
	for i, l := range lines {
		if l == "" {
			lines[i] = st.Render(strings.Repeat(" ", W))
		} else {
			lines[i] = pad + l + pad
		}
	}
	return strings.Join(lines, "\n")
}

// ── dashboard ─────────────────────────────────────────────────────────────

func (m *Model) viewDashboard(W, H int) string {
	alertsH := m.alertsHeight(H)
	mainH := H - alertsH
	var top string
	switch m.tab {
	case tabMain:
		top = m.viewMain(W, mainH)
	case tabNearby:
		top = m.viewNearby(W, mainH)
	case tabMap:
		top = m.viewMap(W, mainH)
	}
	return fitLines(top+"\n"+m.viewAlerts(W, alertsH), W, H)
}

// viewMain shows one location: conditions and hourly on the left, the
// detailed forecast on the right (stacked into one panel when narrow).
func (m *Model) viewMain(W, H int) string {
	if W < wideLayout {
		return m.viewPrimary(W, H, true)
	}
	leftW := clamp(W*11/20, 50, W-40)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.viewPrimary(leftW, H, false),
		m.viewForecast(W-leftW, H))
}

// viewNearby is the grid of every non-primary location, falling back to a
// one-line-per-location list when the cards would be too short.
func (m *Model) viewNearby(W, H int) string {
	others := m.gridIndices()
	if len(others) == 0 {
		return box{title: sBold.Render("Nearby"), body: []string{sDim.Render("Add more locations to config.yaml to see them here.")}, color: colFaint}.render(W, H)
	}
	if cols, rows := gridShape(len(others), W, H); H/rows < 5 || W/cols < 20 {
		return m.viewList(others, W, H)
	}
	return m.viewGrid(others, W, H)
}

// viewList is the compact surrounding-locations panel for narrow terminals.
func (m *Model) viewList(idx []int, W, H int) string {
	m.gridCols = 1
	inner := W - 4
	nameW := 0
	for _, i := range idx {
		nameW = max(nameW, lipgloss.Width(m.locs[i].Cfg.Name))
	}
	// Keep the selected row visible when the list is cut short.
	first := 0
	if pos := m.sel - 1; m.pane == paneLocations && pos >= H-2 {
		first = pos - (H - 2) + 1
	}
	var body []string
	worst := modes.Normal
	for k, i := range idx {
		l := m.locs[i]
		worst = max(worst, l.Mode)
		if k < first {
			continue
		}
		temp := "  —  "
		if l.Obs != nil && l.Obs.TempF != nil {
			t := m.u.Temp(*l.Obs.TempF)
			temp = lipgloss.NewStyle().Foreground(tempColor(*l.Obs.TempF)).Render(strings.Repeat(" ", max(0, 5-lipgloss.Width(t))) + t)
		}
		mark := "  "
		if m.isSelected(i) {
			mark = sAccent.Render("▶ ")
		} else if m.focus == i {
			mark = sAccent.Render("◆ ")
		}
		rest := conditions(l)
		if names := alertNames(l, m.now); len(names) > 0 {
			rest = modeFg(l.Mode).Render(strings.Join(names, ", "))
		}
		row := mark + modeFg(l.Mode).Bold(true).Render(padRight(l.Cfg.Name, nameW)) + " " +
			padRight(badge(l.Mode, true), 12) + " " + temp + "  " + rest
		if m.isSelected(i) {
			row = lipgloss.NewStyle().Background(colSelBg).Render(padRight(row, inner))
		}
		body = append(body, row)
	}
	return box{
		title: sBold.Render("Nearby"),
		body:  body,
		color: modeColors[worst],
		heavy: m.pane == paneLocations && m.sel != 0,
	}.render(W, H)
}

func (m *Model) alertsHeight(H int) int {
	want := len(m.alerts) + 3 // borders + column header
	if len(m.alerts) == 0 {
		want = 3
	}
	return clamp(want, 3, clamp(H/3, 5, 14))
}

// gridIndices are the locations shown in the small grid: everything except
// the primary (index 0), which always has the large panel by default.
func (m *Model) gridIndices() []int {
	var out []int
	for i := 1; i < len(m.locs); i++ {
		out = append(out, i)
	}
	return out
}

func (m *Model) viewGrid(idx []int, W, H int) string {
	if len(idx) == 0 {
		return blank(W, H)
	}
	cols, rows := gridShape(len(idx), W, H)
	m.gridCols = cols

	var rowStrs []string
	for r := 0; r < rows; r++ {
		cellH := H / rows
		if r < H%rows {
			cellH++
		}
		var cells []string
		for c := 0; c < cols; c++ {
			cw := W / cols
			if c < W%cols {
				cw++
			}
			k := r*cols + c
			if k < len(idx) {
				cells = append(cells, m.viewCell(idx[k], cw, cellH))
			} else {
				cells = append(cells, blank(cw, cellH))
			}
		}
		rowStrs = append(rowStrs, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rowStrs...)
}

// gridShape picks columns and rows for n cards in W×H. If cards would be
// shorter than 5 lines, it adds columns while they can stay 20 wide.
func gridShape(n, W, H int) (cols, rows int) {
	cols = clamp(W/cellMinW, 1, n)
	rows = (n + cols - 1) / cols
	for rows > 1 && H/rows < 5 && cols < n && W/(cols+1) >= 20 {
		cols++
		rows = (n + cols - 1) / cols
	}
	return cols, rows
}

func (m *Model) isSelected(i int) bool { return m.pane == paneLocations && m.sel == i }

func (m *Model) viewCell(i, w, h int) string {
	l := m.locs[i]
	inner := w - 4
	col := modeColors[l.Mode]

	b := badge(l.Mode, true)
	temp := "—"
	if l.Obs != nil && l.Obs.TempF != nil {
		t := m.u.Temp(*l.Obs.TempF)
		if lipgloss.Width(b)+1+lipgloss.Width(t) > inner {
			t = strings.TrimSuffix(t, m.u.TempSymbol())
		}
		temp = lipgloss.NewStyle().Foreground(tempColor(*l.Obs.TempF)).Bold(true).Render(t)
	}
	line1 := b + strings.Repeat(" ", max(1, inner-lipgloss.Width(b)-lipgloss.Width(temp))) + temp

	var alertLines []string
	if names := alertNames(l, m.now); len(names) > 0 {
		for _, ln := range wrap(strings.Join(names, ", "), inner) {
			alertLines = append(alertLines, modeFg(l.Mode).Render(ln))
		}
	} else if s := loadState(l); s != "" {
		alertLines = []string{s}
	} else {
		alertLines = []string{sFaint.Render("no alerts")}
	}

	// Extras fill whatever room the alerts leave, in priority order.
	var extras []string
	if hi, lo, ok := todayHiLo(m.u, l.Forecast); ok {
		extras = append(extras, sDim.Render(hi+"  "+lo))
	}
	if o := l.Obs; o != nil {
		hum := "—"
		if o.Humidity != nil {
			hum = fmt.Sprintf("%.0f%%", *o.Humidity)
		}
		extras = append(extras, sDim.Render("Wind ")+sText.Render(windText(m.u, o))+sDim.Render(" · RH ")+sText.Render(hum))
	}
	if sp := sparkline(m.u, l.Hourly, inner-4); sp != "" {
		extras = append(extras, sDim.Render("12h ")+sp)
	}
	room := h - 2 - 2 - len(alertLines)
	extras = extras[:clamp(room, 0, len(extras))]

	body := append([]string{line1, sText.Render(conditions(l))}, extras...)
	body = append(body, alertLines...)

	title := modeFg(l.Mode).Bold(true).Render(l.Cfg.Name)
	if m.focus == i {
		title += sAccent.Render(" ◆")
	}
	return box{
		title:  title,
		footer: m.freshness(l, true),
		body:   body,
		color:  col,
		heavy:  m.isSelected(i),
	}.render(w, h)
}

func (m *Model) viewPrimary(w, h int, withForecast bool) string {
	i := m.focus
	l := m.locs[i]
	inner := w - 4

	var body []string
	if l.Obs == nil {
		body = append(body, "", "  "+loadStateOr(l, sDim.Render("waiting for observations…")))
	} else {
		body = append(body, m.currentBlock(l, inner)...)
	}

	if names := alertNames(l, m.now); len(names) > 0 {
		body = append(body, "")
		for _, ln := range wrap("▲ "+strings.Join(names, ", "), inner) {
			body = append(body, modeFg(l.Mode).Bold(true).Render(ln))
		}
	}

	if len(l.Hourly) > 0 {
		// Without the forecast below, let the bars use the spare height.
		rows := 3
		if !withForecast {
			rows = clamp(h-2-len(body)-5, 3, 10)
		}
		strip := hourlyStrip(m.u, l.Hourly, inner, rows)
		n := min(len(l.Hourly), inner/4)
		body = append(body, "", sAccent.Bold(true).Render(fmt.Sprintf("Next %d hours", n)))
		body = append(body, strip...)
	}
	if withForecast && len(l.Forecast) > 0 {
		body = append(body, "", sAccent.Bold(true).Render("Forecast"))
		for _, p := range l.Forecast {
			body = append(body, forecastLine(m.u, p, inner))
		}
	}

	name := l.Cfg.Name
	if l.Point != nil && l.Point.State != "" {
		name += ", " + l.Point.State
	}
	title := modeFg(l.Mode).Bold(true).Render(name)
	if i != 0 {
		title += sDim.Render(" (esc: back)")
	}
	return box{
		title:  title,
		right:  badge(l.Mode, false),
		footer: m.freshness(l, false),
		body:   body,
		color:  modeColors[l.Mode],
	}.render(w, h)
}

// viewForecast lists the forecast periods with as much of each period's
// detailed text as fits.
func (m *Model) viewForecast(w, h int) string {
	l := m.locs[m.focus]
	inner := w - 4
	var body []string
	if len(l.Forecast) == 0 {
		body = []string{sDim.Render("waiting for forecast…")}
	}
	n := max(1, len(l.Forecast))
	per := (h - 2) / n // lines available per period, including its headline
	for _, p := range l.Forecast {
		body = append(body, forecastLine(m.u, p, inner))
		detail := wrap(p.Detailed, inner-2)
		k := clamp(per-2, 0, len(detail)) // leave a spacer line
		if k < len(detail) && k > 0 {
			detail[k-1] = ansi.Truncate(detail[k-1], inner-3, "") + "…"
		}
		for _, d := range detail[:k] {
			body = append(body, "  "+sDim.Render(d))
		}
		if per >= 3 {
			body = append(body, "")
		}
	}
	return box{
		title: sAccent.Bold(true).Render("Forecast"),
		right: sDim.Render(l.Cfg.Name),
		body:  body,
		color: colFaint,
	}.render(w, h)
}

// currentBlock renders big temperature digits beside the details.
func (m *Model) currentBlock(l *wx.Location, inner int) []string {
	o := l.Obs
	var big []string
	if o.TempF != nil {
		big = bigText(m.u.Temp(*o.TempF), lipgloss.NewStyle().Foreground(tempColor(*o.TempF)).Bold(true))
	} else {
		big = bigText("--°"+m.u.TempSymbol(), sDim)
	}

	feels := "—"
	if o.FeelsLikeF != nil {
		feels = m.u.Temp(*o.FeelsLikeF)
	}
	hum := "—"
	if o.Humidity != nil {
		hum = fmt.Sprintf("%.0f%%", *o.Humidity)
	}
	dew := "—"
	if o.DewpointF != nil {
		dew = m.u.Temp(*o.DewpointF)
	}
	details := []string{
		sBold.Render(conditions(l)),
		sDim.Render("Feels like ") + sText.Render(feels) + sDim.Render("  Humidity ") + sText.Render(hum),
		sDim.Render("Wind ") + sText.Render(windText(m.u, o)) + sDim.Render("  Dew pt ") + sText.Render(dew),
	}

	gap := "   "
	bigW := lipgloss.Width(big[0])
	var out []string
	if bigW+len(gap)+30 <= inner {
		for k := 0; k < 3; k++ {
			out = append(out, big[k]+gap+details[k])
		}
	} else {
		out = append(out, big...)
		out = append(out, details...)
	}

	var extra []string
	if o.PressureInHg != nil {
		extra = append(extra, sDim.Render("Pressure ")+sText.Render(m.u.Pressure(*o.PressureInHg)))
	}
	if o.VisibilityMi != nil {
		extra = append(extra, sDim.Render("Vis ")+sText.Render(m.u.Distance(*o.VisibilityMi)))
	}
	if o.Precip3hIn != nil {
		extra = append(extra, sDim.Render("Precip 3h ")+sText.Render(m.u.Precip(*o.Precip3hIn)))
	}
	if len(extra) > 0 {
		out = append(out, strings.Join(extra, "  "))
	}
	stn := o.StationID
	if o.StationName != "" && o.StationName != stn {
		stn += " " + o.StationName
	}
	out = append(out, sFaint.Render(fmt.Sprintf("Station %s · observed %s", stn, m.u.Clock(o.Time))))
	return out
}

func (m *Model) viewAlerts(W, H int) string {
	inner := W - 4
	focused := m.pane == paneAlerts
	var body []string
	if len(m.alerts) == 0 {
		msg := sDim.Render("No active alerts for any location.")
		if m.anyAlertsLoaded() {
			msg = modeFg(modes.Normal).Render("✓ ") + msg
		} else {
			msg = sDim.Render("Loading alerts…")
		}
		body = []string{msg}
	} else {
		// Columns: mode │ event │ expires │ areas │ headline
		const modeW, eventW, expW = 14, 26, 16
		rest := max(0, inner-modeW-eventW-expW-3)
		areaW, headW := rest, 0
		if rest >= 50 {
			areaW = rest * 2 / 5
			headW = rest - areaW - 1
		}
		hdr := "  " + padRight("MODE", modeW-2) + " " + padRight("EVENT", eventW) + " " + padRight("EXPIRES", expW) + " "
		if areaW > 6 {
			hdr += padRight("AREAS", areaW)
		}
		if headW > 0 {
			hdr += " " + padRight("HEADLINE", headW)
		}
		body = append(body, sFaint.Bold(true).Render(hdr))

		visible := H - 3
		if m.alertSel < m.alertOff {
			m.alertOff = m.alertSel
		}
		if m.alertSel >= m.alertOff+visible {
			m.alertOff = m.alertSel - visible + 1
		}
		m.alertOff = clamp(m.alertOff, 0, max(0, len(m.alerts)-visible))

		for k := m.alertOff; k < len(m.alerts) && k < m.alertOff+visible; k++ {
			e := m.alerts[k]
			sel := focused && k == m.alertSel
			mark := "  "
			if sel {
				mark = sAccent.Render("▶ ")
			}
			row := mark + padRight(badge(e.Mode, true), modeW-2) + " " +
				modeFg(e.Mode).Bold(true).Render(padRight(e.Alert.Event, eventW)) + " " +
				sText.Render(padRight(fmtWhen(m.u, e.Alert.EndTime(), m.now), expW)) + " "
			if areaW > 6 {
				row += sDim.Render(padRight(e.Alert.AreaDesc, areaW))
			}
			if headW > 0 {
				row += " " + sText.Render(padRight(e.Alert.Headline, headW))
			}
			if sel {
				row = lipgloss.NewStyle().Background(colSelBg).Render(padRight(row, inner))
			}
			body = append(body, row)
		}
	}

	title := sBold.Render("Alerts")
	if n := len(m.alerts); n > 0 {
		title += sDim.Render(fmt.Sprintf(" (%d)", n))
	}
	footer := ""
	if focused {
		footer = sDim.Render("enter: details · ↑↓ select")
	} else if len(m.alerts) > H-3 {
		footer = sDim.Render("a: browse")
	}
	worst := modes.Normal
	if len(m.alerts) > 0 {
		worst = m.alerts[0].Mode
	}
	color := colFaint
	if focused || worst > modes.Normal {
		color = modeColors[worst]
	}
	return box{title: title, footer: footer, body: body, color: color, heavy: focused}.render(W, H)
}

func (m *Model) anyAlertsLoaded() bool {
	for _, l := range m.locs {
		if !l.AlertsUpdated.IsZero() {
			return true
		}
	}
	return false
}

// ── expanded alert & help ─────────────────────────────────────────────────

func (m *Model) alertDetail(e wx.AlertEntry) string {
	a := e.Alert
	w := m.vp.Width
	var b strings.Builder
	line := func(label, v string) {
		if v != "" {
			b.WriteString(sDim.Render(fmt.Sprintf("%-11s", label)) + sText.Render(v) + "\n")
		}
	}
	b.WriteString(badge(e.Mode, false) + "  " + modeFg(e.Mode).Bold(true).Render(a.Event) + "\n\n")
	if a.Headline != "" {
		b.WriteString(sBold.Render(strings.Join(wrap(a.Headline, w), "\n")) + "\n\n")
	}
	line("Locations", strings.Join(e.Locations, ", "))
	line("Severity", strings.Join(nonEmpty(a.Severity, a.Urgency, a.Certainty), " · "))
	line("Issued by", a.SenderName)
	line("Sent", fmtTime(m.u, a.Sent))
	line("Onset", fmtTime(m.u, a.Onset))
	line("Expires", fmtTime(m.u, a.Expires))
	line("Ends", fmtTime(m.u, a.Ends))
	b.WriteString("\n" + sDim.Render("Areas") + "\n" + sText.Render(strings.Join(wrap(a.AreaDesc, w), "\n")) + "\n")
	if a.Description != "" {
		b.WriteString("\n" + sAccent.Bold(true).Render("Description") + "\n" + sText.Render(wrapParas(a.Description, w)) + "\n")
	}
	if a.Instruction != "" {
		b.WriteString("\n" + modeFg(e.Mode).Bold(true).Render("Instructions") + "\n" + sBold.Render(wrapParas(a.Instruction, w)) + "\n")
	}
	return b.String()
}

func (m *Model) viewExpanded(W, H int) string {
	e := m.alerts[clamp(m.alertSel, 0, len(m.alerts)-1)]
	m.vp.Width, m.vp.Height = W-4, H-2
	pct := fmt.Sprintf("%3.0f%%", m.vp.ScrollPercent()*100)
	return box{
		title:  modeFg(e.Mode).Bold(true).Render(e.Alert.Event),
		right:  sDim.Render(pct),
		footer: sDim.Render("↑↓/pgup/pgdn scroll · esc close"),
		body:   strings.Split(m.vp.View(), "\n"),
		color:  modeColors[e.Mode],
		heavy:  true,
	}.render(W, H)
}

func (m *Model) viewHelp(maxW int) string {
	rows := [][2]string{
		{"q / ctrl+c", "quit"},
		{"r", "refresh everything now"},
		{"1 2 3", "Main / Nearby / Map tabs"},
		{"tab / shift+tab", "next / previous tab"},
		{"← → ↑ ↓ / hjkl", "move between locations (Map: nearest in that direction)"},
		{"enter", "open selected location on Main / expand alert"},
		{"esc", "back to the primary location / leave alerts / close"},
		{"a", "jump to the alerts pane"},
		{"v", "Map: cycle layer (temperature, threat, rain chance)"},
		{"?", "toggle this help"},
	}
	if m.demo {
		rows = append(rows, [2]string{"m", "demo: cycle the selected location's mode"}, [2]string{"M", "demo: cycle all locations together"})
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, sAccent.Render(padRight(r[0], 18))+sText.Render(r[1]))
	}
	lines = append(lines, "",
		sDim.Render("Modes: ")+badge(modes.Normal, false)+" "+badge(modes.Warning, false)+" "+badge(modes.Heightened, false)+" "+badge(modes.TakeCover, false),
		"",
		sDim.Render(fmt.Sprintf("Alerts refresh every %s, observations/forecasts every %s.", m.cfg.Refresh.Alerts, m.cfg.Refresh.Forecast)),
		sDim.Render("Mode changes are logged to ")+sText.Render(m.cfg.LogPath()),
		"", sFaint.Render("press any key to close"))
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	w = min(w+4, maxW)
	return box{title: sBold.Render("Help"), body: lines, color: colAccent}.render(w, len(lines)+2)
}

// ── pieces ────────────────────────────────────────────────────────────────

// freshness renders a panel footer: last update time, or a stale marker.
func (m *Model) freshness(l *wx.Location, compact bool) string {
	upd := l.WxUpdated
	if l.AlertsUpdated.After(upd) {
		upd = l.AlertsUpdated
	}
	alertsStale := wx.Stale(l.AlertsUpdated, l.AlertsErr, m.cfg.Refresh.Alerts, m.now)
	wxStale := wx.Stale(l.WxUpdated, l.WxErr, m.cfg.Refresh.Forecast, m.now)
	if compact {
		switch {
		case alertsStale || wxStale:
			return sStale.Render("STALE") + sDim.Render(" "+m.u.Clock(upd))
		case upd.IsZero():
			return ""
		}
		return sFaint.Render(m.u.Clock(upd))
	}
	part := func(name string, t time.Time, stale bool) string {
		if t.IsZero() {
			return sDim.Render(name + " —")
		}
		s := sDim.Render(name+" ") + sText.Render(m.u.Clock(t))
		if stale {
			s += " " + sStale.Render("STALE")
		}
		return s
	}
	return part("alerts", l.AlertsUpdated, alertsStale) + sFaint.Render(" · ") + part("wx", l.WxUpdated, wxStale)
}

// loadState describes a location that has no data yet, or "".
func loadState(l *wx.Location) string {
	switch {
	case errors.Is(l.PointErr, api.ErrNoCoverage):
		return sErr.Render("outside NWS coverage")
	case l.Point == nil && l.PointErr != nil:
		return sErr.Render("lookup failed, retrying")
	case l.Obs == nil && l.WxErr != nil:
		return sErr.Render("weather unavailable")
	case l.AlertsUpdated.IsZero() && l.AlertsErr != nil:
		return sErr.Render("alerts unavailable")
	case l.Obs == nil:
		return sDim.Render("loading…")
	}
	return ""
}

func loadStateOr(l *wx.Location, def string) string {
	if s := loadState(l); s != "" {
		return s
	}
	return def
}

// conditions prefers the observed sky description, falling back to the
// current hourly forecast (many nearby stations don't report sky).
func conditions(l *wx.Location) string {
	if l.Obs != nil && l.Obs.Description != "" {
		return l.Obs.Description
	}
	if len(l.Hourly) > 0 && l.Hourly[0].Short != "" {
		return l.Hourly[0].Short
	}
	return "—"
}

func alertNames(l *wx.Location, now time.Time) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range l.AllAlerts(now) {
		if !seen[a.Event] {
			seen[a.Event] = true
			out = append(out, a.Event)
		}
	}
	return out
}

func todayHiLo(u units.Units, ps []api.Period) (hi, lo string, ok bool) {
	for _, p := range ps {
		if p.IsDaytime && hi == "" {
			hi = fmt.Sprintf("↑%d°", u.DegInt(p.Temperature))
		}
		if !p.IsDaytime && lo == "" {
			lo = fmt.Sprintf("↓%d°", u.DegInt(p.Temperature))
		}
	}
	return hi, lo, hi != "" || lo != ""
}

func windText(u units.Units, o *api.Observation) string {
	if o.WindMph == nil {
		return "—"
	}
	if math.Round(*o.WindMph) == 0 {
		return "calm"
	}
	s := u.Speed(*o.WindMph)
	if o.WindDirDeg != nil {
		s = api.Compass(*o.WindDirDeg) + " " + s
	}
	if o.GustMph != nil {
		s += fmt.Sprintf(" g%.0f", u.SpeedValue(*o.GustMph))
	}
	return s
}

func forecastLine(u units.Units, p api.Period, w int) string {
	arrow, col := "↓", lipgloss.Color("#93c5fd")
	if p.IsDaytime {
		arrow, col = "↑", tempColor(float64(p.Temperature))
	}
	temp := lipgloss.NewStyle().Foreground(col).Bold(true).Render(fmt.Sprintf("%s%3d°", arrow, u.DegInt(p.Temperature)))
	pop := "    "
	if p.PrecipChance != nil && *p.PrecipChance > 0 {
		pop = sRain.Render(fmt.Sprintf("%3.0f%%", *p.PrecipChance))
	}
	nameW := clamp(w/4, 10, 16)
	short := p.Short
	return sText.Render(padRight(p.Name, nameW)) + " " + temp + " " + pop + "  " + sDim.Render(ansi.Truncate(short, max(0, w-nameW-13), "…"))
}

// hourlyStrip draws temperature bars (rows tall) for the next hours with
// temp labels above and hour / precip labels below.
func hourlyStrip(u units.Units, hrs []api.Period, w, rows int) []string {
	const colW = 4
	n := min(len(hrs), w/colW)
	if n == 0 {
		return nil
	}
	hrs = hrs[:n]
	lo, hi := hrs[0].Temperature, hrs[0].Temperature
	for _, h := range hrs {
		lo, hi = min(lo, h.Temperature), max(hi, h.Temperature)
	}
	levels := rows * 8
	blocks := []rune(" ▁▂▃▄▅▆▇█")

	var temps, hours, pops strings.Builder
	bars := make([]strings.Builder, rows)
	anyPop := false
	for _, h := range hrs {
		st := lipgloss.NewStyle().Foreground(tempColor(float64(h.Temperature)))
		lvl := levels / 2
		if hi > lo {
			lvl = 2 + (h.Temperature-lo)*(levels-2)/(hi-lo)
		}
		temps.WriteString(st.Render(padRight(fmt.Sprintf("%d°", u.DegInt(h.Temperature)), colW)))
		for r := 0; r < rows; r++ {
			fill := clamp(lvl-(rows-1-r)*8, 0, 8)
			bars[r].WriteString(st.Render(strings.Repeat(string(blocks[fill]), colW-1)) + " ")
		}
		hours.WriteString(sDim.Render(padRight(u.Hour(h.Start.Local()), colW)))
		p := ""
		if h.PrecipChance != nil && *h.PrecipChance >= 10 {
			p = fmt.Sprintf("%.0f%%", *h.PrecipChance)
			if *h.PrecipChance >= 99.5 {
				p = "100" // "100%" would touch the next column
			}
			anyPop = true
		}
		pops.WriteString(sRain.Render(padRight(p, colW)))
	}
	out := []string{temps.String()}
	for r := range bars {
		out = append(out, bars[r].String())
	}
	out = append(out, hours.String())
	if anyPop {
		out = append(out, pops.String())
	}
	return out
}

// sparkline is a one-row temperature trend for the next 12 hours.
func sparkline(u units.Units, hrs []api.Period, w int) string {
	n := min(len(hrs), 12, w)
	if n < 2 {
		return ""
	}
	hrs = hrs[:n]
	lo, hi := hrs[0].Temperature, hrs[0].Temperature
	for _, h := range hrs {
		lo, hi = min(lo, h.Temperature), max(hi, h.Temperature)
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, h := range hrs {
		k := 3
		if hi > lo {
			k = (h.Temperature - lo) * 7 / (hi - lo)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(tempColor(float64(h.Temperature))).Render(string(blocks[k])))
	}
	return b.String() + sDim.Render(fmt.Sprintf(" %d–%d°", u.DegInt(lo), u.DegInt(hi)))
}

// bigText renders digits, '-', '°', 'F' and 'C' in a 3-row block font.
func bigText(s string, st lipgloss.Style) []string {
	font := map[rune][3]string{
		'0': {"█▀█", "█ █", "▀▀▀"},
		'1': {"▀█ ", " █ ", "▀▀▀"},
		'2': {"▀▀█", "█▀▀", "▀▀▀"},
		'3': {"▀▀█", " ▀█", "▀▀▀"},
		'4': {"█ █", "▀▀█", "  ▀"},
		'5': {"█▀▀", "▀▀█", "▀▀▀"},
		'6': {"█▀▀", "█▀█", "▀▀▀"},
		'7': {"▀▀█", "  █", "  ▀"},
		'8': {"█▀█", "█▀█", "▀▀▀"},
		'9': {"█▀█", "▀▀█", "▀▀▀"},
		'-': {"   ", "▀▀▀", "   "},
		'°': {"▀", " ", " "},
		'F': {"█▀▀", "█▀ ", "▀  "},
		'C': {"█▀▀", "█  ", "▀▀▀"},
	}
	var rows [3]strings.Builder
	for _, r := range s {
		g, ok := font[r]
		if !ok {
			continue
		}
		for k := 0; k < 3; k++ {
			rows[k].WriteString(g[k] + " ")
		}
	}
	return []string{st.Render(rows[0].String()), st.Render(rows[1].String()), st.Render(rows[2].String())}
}

// ── formatting helpers ────────────────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return max(lo, min(hi, v))
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// fmtWhen is a compact expiry: "3:00 PM", "Sat 5:00 AM".
func fmtWhen(u units.Units, t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	t = t.Local()
	if y, mo, d := t.Date(); y == now.Year() && mo == now.Month() && d == now.Day() {
		return t.Format(u.Layout("{clock}"))
	}
	return t.Format(u.Layout("Mon {clock}"))
}

func fmtTime(u units.Units, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(u.Layout("Mon Jan 2, {clock} MST"))
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// wrapParas wraps NWS text, which hard-wraps lines at ~70 columns, by
// rejoining lines within each paragraph before wrapping to width.
func wrapParas(s string, w int) string {
	paras := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n")
	for i, p := range paras {
		paras[i] = strings.Join(wrap(strings.Join(strings.Fields(p), " "), w), "\n")
	}
	return strings.Join(paras, "\n\n")
}

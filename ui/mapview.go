package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jdankney/ttywx/api"
	"github.com/jdankney/ttywx/modes"
	"github.com/jdankney/ttywx/units"
	"github.com/jdankney/ttywx/wx"
)

// The Map tab plots every location at its real position over a colored
// field interpolated between them (inverse-distance weighting). Each
// terminal cell holds two vertically stacked "pixels" drawn with ▀, which
// makes pixels roughly square.

type mapLayer int

const (
	layerTemp mapLayer = iota
	layerThreat
	layerRain
	numLayers
)

func (l mapLayer) String() string {
	return [...]string{"Temperature", "Threat", "Rain chance"}[l]
}

// ── colors ────────────────────────────────────────────────────────────────

type rgb struct{ r, g, b float64 }

func hexRGB(s string) rgb {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	return rgb{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}
}

func (c rgb) hex() string {
	cl := func(f float64) int { return clamp(int(math.Round(f)), 0, 255) }
	return fmt.Sprintf("#%02x%02x%02x", cl(c.r), cl(c.g), cl(c.b))
}

func (c rgb) scale(f float64) rgb { return rgb{c.r * f, c.g * f, c.b * f} }

type stop struct {
	v float64
	c rgb
}

func stops(pairs ...any) []stop {
	var out []stop
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, stop{float64(pairs[i].(int)), hexRGB(pairs[i+1].(string))})
	}
	return out
}

var (
	// °F. Wide enough for a northern winter and a desert summer.
	tempStops = stops(-10, "#c4b5fd", 10, "#818cf8", 30, "#3b82f6", 45, "#22d3ee", 60, "#4ade80",
		72, "#facc15", 85, "#fb923c", 100, "#ef4444", 115, "#be123c")
	rainStops = stops(0, "#334155", 30, "#1d4ed8", 60, "#2563eb", 100, "#93c5fd")
	mapEmpty  = hexRGB("#111827")
	labelBg   = "#0b1220"
)

func gradient(st []stop, v float64) rgb {
	if v <= st[0].v {
		return st[0].c
	}
	for i := 1; i < len(st); i++ {
		if v <= st[i].v {
			t := (v - st[i-1].v) / (st[i].v - st[i-1].v)
			a, b := st[i-1].c, st[i].c
			return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
		}
	}
	return st[len(st)-1].c
}

// ── geometry ──────────────────────────────────────────────────────────────

type point struct{ x, y float64 } // km east / north of the locations' centroid

func (m *Model) geoKm() []point {
	var lat0, lon0 float64
	for _, l := range m.locs {
		lat0 += l.Cfg.Lat
		lon0 += l.Cfg.Lon
	}
	n := float64(len(m.locs))
	lat0, lon0 = lat0/n, lon0/n
	k := math.Cos(lat0 * math.Pi / 180)
	pts := make([]point, len(m.locs))
	for i, l := range m.locs {
		pts[i] = point{(l.Cfg.Lon - lon0) * 111.32 * k, (l.Cfg.Lat - lat0) * 110.57}
	}
	return pts
}

// projection maps km to pixel coordinates for a px×py canvas.
type projection struct {
	cx, cy, s float64 // center (km) and scale (pixels per km)
	px, py    int
}

func newProjection(pts []point, px, py int) projection {
	minX, maxX, minY, maxY := pts[0].x, pts[0].x, pts[0].y, pts[0].y
	for _, p := range pts {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	spanX := math.Max(maxX-minX, 10) * 1.3 // 15% padding each side
	spanY := math.Max(maxY-minY, 10) * 1.3
	s := math.Min(float64(px-1)/spanX, float64(py-1)/spanY)
	return projection{(minX + maxX) / 2, (minY + maxY) / 2, s, px, py}
}

func (pr projection) toPix(p point) (int, int) {
	x := float64(pr.px)/2 + (p.x-pr.cx)*pr.s
	y := float64(pr.py)/2 - (p.y-pr.cy)*pr.s
	return int(math.Floor(x)), int(math.Floor(y))
}

func (pr projection) toKm(x, y int) point {
	return point{
		pr.cx + (float64(x)+0.5-float64(pr.px)/2)/pr.s,
		pr.cy - (float64(y)+0.5-float64(pr.py)/2)/pr.s,
	}
}

// ── layer values ──────────────────────────────────────────────────────────

func (m *Model) layerValue(l *wx.Location) (float64, bool) {
	switch m.mapLayer {
	case layerTemp:
		if l.Obs != nil && l.Obs.TempF != nil {
			return *l.Obs.TempF, true
		}
	case layerRain:
		if len(l.Hourly) > 0 && l.Hourly[0].PrecipChance != nil {
			return *l.Hourly[0].PrecipChance, true
		}
	}
	return 0, false
}

func (m *Model) layerLabel(l *wx.Location) string {
	switch m.mapLayer {
	case layerThreat:
		return l.Mode.Short()
	case layerRain:
		if v, ok := m.layerValue(l); ok {
			return fmt.Sprintf("%.0f%%", v)
		}
	default:
		if v, ok := m.layerValue(l); ok {
			s := m.u.Deg(v)
			if a := windArrow(m.u, l.Obs); a != "" {
				s += " " + a
			}
			return s
		}
	}
	return "—"
}

// windArrow points where the wind blows to (NWS reports where it's from).
func windArrow(u units.Units, o *api.Observation) string {
	if o == nil || o.WindDirDeg == nil || o.WindMph == nil || *o.WindMph < 0.5 {
		return ""
	}
	arrows := [...]string{"↑", "↗", "→", "↘", "↓", "↙", "←", "↖"}
	to := math.Mod(*o.WindDirDeg+180, 360)
	return arrows[int(math.Round(to/45))%8] + fmt.Sprintf("%.0f", u.SpeedValue(*o.WindMph))
}

// fieldColor is the background color at a point for the current layer.
func (m *Model) fieldColor(p point, pts []point) rgb {
	if m.mapLayer == layerThreat {
		best, bd := 0, math.Inf(1)
		for i, q := range pts {
			if d := math.Hypot(p.x-q.x, p.y-q.y); d < bd {
				best, bd = i, d
			}
		}
		return hexRGB(string(modeColors[m.locs[best].Mode])).scale(0.38)
	}
	var sum, wsum float64
	for i, q := range pts {
		v, ok := m.layerValue(m.locs[i])
		if !ok {
			continue
		}
		d2 := (p.x-q.x)*(p.x-q.x) + (p.y-q.y)*(p.y-q.y)
		w := 1 / (d2 + 0.25)
		sum += v * w
		wsum += w
	}
	if wsum == 0 {
		return mapEmpty
	}
	return m.layerColor(sum / wsum)
}

func (m *Model) layerColor(v float64) rgb {
	if m.mapLayer == layerRain {
		return gradient(rainStops, v).scale(0.6)
	}
	return gradient(tempStops, v).scale(0.5)
}

// ── rendering ─────────────────────────────────────────────────────────────

type overlay struct {
	text     string
	fg, bg   string // bg "" keeps the map color underneath
	bold, on bool
}

func (m *Model) viewMap(W, H int) string {
	side := 0
	if W >= wideLayout {
		side = clamp(W/4, 30, 40)
	}
	mapW := W - side
	cw, ch := mapW-4, H-3 // canvas cells; one body row is the legend
	var body []string
	if cw < 10 || ch < 3 {
		body = []string{sDim.Render("make the terminal taller to see the map")}
	} else {
		body = append(m.renderCanvas(cw, ch), m.mapLegend(cw))
	}
	mapBox := box{
		title:  sAccent.Bold(true).Render("Map"),
		right:  sDim.Render(m.mapLayer.String()),
		footer: m.mapFooter(side == 0),
		body:   body,
		color:  colFaint,
		heavy:  m.pane == paneLocations,
	}.render(mapW, H)
	if side == 0 {
		return mapBox
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, mapBox, m.viewMapSide(side, H))
}

// mapFooter names the selection when there's no side panel to show it.
func (m *Model) mapFooter(compact bool) string {
	if !compact {
		return ""
	}
	l := m.locs[m.sel]
	return modeFg(l.Mode).Bold(true).Render(l.Cfg.Name) + sDim.Render(" · enter: open")
}

func (m *Model) renderCanvas(cw, ch int) []string {
	pts := m.geoKm()
	pr := newProjection(pts, cw, ch*2)

	pix := make([][]rgb, ch*2)
	for y := range pix {
		pix[y] = make([]rgb, cw)
		for x := range pix[y] {
			pix[y][x] = m.fieldColor(pr.toKm(x, y), pts)
		}
	}

	ov := make([][]overlay, ch)
	for r := range ov {
		ov[r] = make([]overlay, cw)
	}
	free := func(r, c, n int) bool {
		if r < 0 || r >= ch || c < 0 || c+n > cw {
			return false
		}
		for k := c; k < c+n; k++ {
			if ov[r][k].on {
				return false
			}
		}
		return true
	}
	put := func(r, c int, s, fg, bg string, bold bool) {
		for k, ch := range []rune(s) {
			if c+k < cw {
				ov[r][c+k] = overlay{string(ch), fg, bg, bold, true}
			}
		}
	}

	// Markers first so labels avoid them.
	type mk struct{ i, r, c int }
	var marks []mk
	for i, p := range pts {
		x, y := pr.toPix(p)
		r, c := clamp(y/2, 0, ch-1), clamp(x, 0, cw-1)
		marks = append(marks, mk{i, r, c})
		dot, fg := "●", string(modeColors[m.locs[i].Mode])
		if i == m.sel && m.tab == tabMap {
			dot, fg = "◉", "#ffffff"
		}
		put(r, c, dot, fg, "", true)
	}

	// Labels: selected, then primary, then the rest; try right, left,
	// above, below, and fall back to a shorter label.
	order := []int{m.sel}
	if m.sel != 0 {
		order = append(order, 0)
	}
	for i := range m.locs {
		if i != m.sel && i != 0 {
			order = append(order, i)
		}
	}
	for _, i := range order {
		l, mkr := m.locs[i], marks[i]
		val := m.layerLabel(l)
		for _, text := range []string{" " + l.Cfg.Name + " " + val + " ", " " + val + " "} {
			n := len([]rune(text))
			spots := [][2]int{{mkr.r, mkr.c + 1}, {mkr.r, mkr.c - n}, {mkr.r - 1, mkr.c - n/2}, {mkr.r + 1, mkr.c - n/2}}
			placed := false
			for _, sp := range spots {
				if free(sp[0], sp[1], n) {
					fg := "#e5e7eb"
					if i == m.sel && m.tab == tabMap {
						fg = string(colAccent)
					}
					put(sp[0], sp[1], text, fg, labelBg, i == m.sel || i == 0)
					placed = true
					break
				}
			}
			if placed {
				break
			}
		}
	}

	// North arrow and scale bar.
	if free(0, cw-3, 3) {
		put(0, cw-3, "N↑ ", "#cbd5e1", labelBg, true)
	}
	kmPerCell := 1 / pr.s
	unit, kmPerUnit := m.u.DistanceUnit()
	for _, d := range []float64{1000, 500, 250, 200, 100, 50, 25, 20, 10, 5, 2, 1} {
		n := int(math.Round(d * kmPerUnit / kmPerCell))
		if n >= 3 && n <= cw/4 {
			bar := "├" + strings.Repeat("─", n-2) + "┤" + fmt.Sprintf(" %g %s ", d, unit)
			if free(ch-1, 1, len([]rune(bar))) {
				put(ch-1, 1, bar, "#cbd5e1", labelBg, false)
			}
			break
		}
	}

	// Emit rows, batching runs that share a style.
	lines := make([]string, ch)
	for r := 0; r < ch; r++ {
		var b, run strings.Builder
		var curFg, curBg string
		var curBold bool
		flush := func() {
			if run.Len() > 0 {
				st := lipgloss.NewStyle().Foreground(lipgloss.Color(curFg)).Background(lipgloss.Color(curBg)).Bold(curBold)
				b.WriteString(st.Render(run.String()))
				run.Reset()
			}
		}
		for c := 0; c < cw; c++ {
			text, fg, bg, bold := "▀", pix[2*r][c].hex(), pix[2*r+1][c].hex(), false
			if o := ov[r][c]; o.on {
				text, fg, bold = o.text, o.fg, o.bold
				if o.bg != "" {
					bg = o.bg
				} else {
					bg = pix[2*r][c].hex()
				}
			}
			if fg != curFg || bg != curBg || bold != curBold {
				flush()
				curFg, curBg, curBold = fg, bg, bold
			}
			run.WriteString(text)
		}
		flush()
		lines[r] = b.String()
	}
	return lines
}

func (m *Model) mapLegend(w int) string {
	switch m.mapLayer {
	case layerThreat:
		return badge(modes.Normal, true) + " " + badge(modes.Warning, true) + " " +
			badge(modes.Heightened, true) + " " + badge(modes.TakeCover, true) +
			sDim.Render("  areas colored by the nearest location's mode")
	}
	lo, hi, have := math.Inf(1), math.Inf(-1), false
	for _, l := range m.locs {
		if v, ok := m.layerValue(l); ok {
			lo, hi, have = math.Min(lo, v), math.Max(hi, v), true
		}
	}
	if !have {
		return sDim.Render("no data for this layer yet")
	}
	label := m.u.Deg
	if m.mapLayer == layerRain {
		label = func(v float64) string { return fmt.Sprintf("%.0f%%", v) }
	}
	const n = 24
	var bar strings.Builder
	for k := 0; k < n; k++ {
		v := lo + (hi-lo)*float64(k)/float64(n-1)
		bar.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(m.layerColor(v).scale(1.6).hex())).Render("█"))
	}
	s := sText.Render(label(lo)+" ") + bar.String() + sText.Render(" "+label(hi))
	if m.mapLayer == layerTemp {
		s += sDim.Render("   arrows: wind (blowing toward), " + m.u.SpeedUnit())
	} else {
		s += sDim.Render("   chance of rain this hour")
	}
	return s
}

// viewMapSide shows the selected location and a list of every place.
func (m *Model) viewMapSide(w, h int) string {
	l := m.locs[m.sel]
	inner := w - 4
	body := []string{badge(l.Mode, false)}
	temp := "—"
	if l.Obs != nil && l.Obs.TempF != nil {
		temp = lipgloss.NewStyle().Foreground(tempColor(*l.Obs.TempF)).Bold(true).Render(m.u.Temp(*l.Obs.TempF))
		if l.Obs.FeelsLikeF != nil {
			temp += sDim.Render("  feels " + m.u.Temp(*l.Obs.FeelsLikeF))
		}
	}
	body = append(body, temp, sText.Render(conditions(l)))
	if o := l.Obs; o != nil {
		hum := "—"
		if o.Humidity != nil {
			hum = fmt.Sprintf("%.0f%%", *o.Humidity)
		}
		body = append(body, sDim.Render("Wind ")+sText.Render(windText(m.u, o)), sDim.Render("Humidity ")+sText.Render(hum))
	}
	if hi, lo, ok := todayHiLo(m.u, l.Forecast); ok {
		body = append(body, sDim.Render("Forecast ")+sText.Render(hi+" "+lo))
	}
	if len(l.Hourly) > 0 && l.Hourly[0].PrecipChance != nil {
		body = append(body, sDim.Render("Rain this hour ")+sRain.Render(fmt.Sprintf("%.0f%%", *l.Hourly[0].PrecipChance)))
	}
	if names := alertNames(l, m.now); len(names) > 0 {
		body = append(body, "")
		for _, ln := range wrap(strings.Join(names, ", "), inner) {
			body = append(body, modeFg(l.Mode).Render(ln))
		}
	}

	body = append(body, "", sAccent.Bold(true).Render("Places"))
	for i, p := range m.locs {
		mark := "  "
		if i == m.sel {
			mark = sAccent.Render("▶ ")
		}
		t := ""
		if p.Obs != nil && p.Obs.TempF != nil {
			t = m.u.Deg(*p.Obs.TempF)
		}
		name := padRight(p.Cfg.Name, inner-2-2-5)
		row := mark + modeFg(p.Mode).Render("● ") + sText.Render(name) + sDim.Render(fmt.Sprintf("%5s", t))
		body = append(body, row)
	}
	return box{
		title:  modeFg(l.Mode).Bold(true).Render(l.Cfg.Name),
		footer: sDim.Render("enter: open in Main"),
		body:   body,
		color:  modeColors[l.Mode],
	}.render(w, h)
}

// mapNav moves the selection to the nearest location in a screen
// direction (dx: +1 east, dy: +1 south). It reports whether it moved.
func (m *Model) mapNav(dx, dy int) bool {
	pts := m.geoKm()
	cur := pts[m.sel]
	dirX, dirY := float64(dx), float64(-dy)
	best, bestScore := -1, math.Inf(1)
	for j, p := range pts {
		vx, vy := p.x-cur.x, p.y-cur.y
		d := math.Hypot(vx, vy)
		if j == m.sel || d == 0 {
			continue
		}
		cos := (vx*dirX + vy*dirY) / d
		if cos < 0.35 {
			continue // not really in that direction
		}
		if score := d * (2 - cos); score < bestScore {
			best, bestScore = j, score
		}
	}
	if best < 0 {
		return false
	}
	m.sel = best
	return true
}

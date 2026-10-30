// Package ui is the full-screen Bubble Tea dashboard.
package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/j4mes/ttywx/api"
	"github.com/j4mes/ttywx/config"
	"github.com/j4mes/ttywx/demo"
	"github.com/j4mes/ttywx/modes"
	"github.com/j4mes/ttywx/tracker"
	"github.com/j4mes/ttywx/units"
	"github.com/j4mes/ttywx/wx"
)

const (
	pointRetry   = 30 * time.Second
	fetchTimeout = 90 * time.Second // per fetch, including retries
	frameTick    = 500 * time.Millisecond
)

type pane int

const (
	paneLocations pane = iota
	paneAlerts
)

type tab int

const (
	tabMain tab = iota
	tabNearby
	tabMap
	numTabs
)

func (t tab) String() string {
	return [...]string{"Main", "Nearby", "Map"}[t]
}

type Model struct {
	cfg     *config.Config
	u       units.Units // display units and clock, from cfg
	client  *api.Client
	tracker *tracker.Tracker
	ctx     context.Context
	cancel  context.CancelFunc

	locs []*wx.Location
	now  time.Time

	width, height int
	gridCols      int // columns in the surrounding-locations grid, from the last layout

	tab      tab
	pane     pane
	sel      int // selected location on the Nearby / Map tabs
	focus    int // location shown on the Main tab
	mapLayer mapLayer
	alertSel int
	alertOff int
	alerts   []wx.AlertEntry // merged, sorted; rebuilt on every change

	help     bool
	expanded bool
	vp       viewport.Model

	demo      bool
	demoModes map[int]modes.Mode

	flash bool

	// refresh generations: bumping one orphans the pending tick chain so a
	// forced refresh restarts the interval instead of doubling up.
	alertGen, wxGen int
	alertBusy       []bool
	wxBusy          []bool
	pointBusy       []bool
}

// New builds the dashboard model.
func New(cfg *config.Config, client *api.Client, tr *tracker.Tracker, demoMode bool) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{
		cfg: cfg, u: cfg.Display(), client: client, tracker: tr,
		ctx: ctx, cancel: cancel,
		now:       time.Now(),
		gridCols:  2,
		demo:      demoMode,
		alertBusy: make([]bool, len(cfg.Locations)),
		wxBusy:    make([]bool, len(cfg.Locations)),
		pointBusy: make([]bool, len(cfg.Locations)),
	}
	for _, l := range cfg.Locations {
		m.locs = append(m.locs, &wx.Location{Cfg: l})
	}
	if len(m.locs) > 1 {
		m.sel = 1
	}
	if demoMode {
		m.demoModes = demo.Initial(len(m.locs))
		m.applyDemo()
	}
	return m
}

// ── messages ──────────────────────────────────────────────────────────────

type pointMsg struct {
	i   int
	pt  *api.Point
	err error
}
type retryPointMsg struct{ i int }
type alertsMsg struct {
	i      int
	alerts []api.Alert
	err    error
}
type wxMsg struct {
	i   int
	res wx.WeatherResult
}
type frameMsg time.Time
type alertTickMsg struct{ gen int }
type wxTickMsg struct{ gen int }

// ── commands ──────────────────────────────────────────────────────────────

func (m *Model) resolvePoint(i int) tea.Cmd {
	if m.pointBusy[i] {
		return nil
	}
	m.pointBusy[i] = true
	loc := m.locs[i].Cfg
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, fetchTimeout)
		defer cancel()
		pt, err := m.client.ResolvePoint(ctx, loc.Lat, loc.Lon)
		return pointMsg{i, pt, err}
	}
}

func (m *Model) fetchAlerts(i int) tea.Cmd {
	if m.alertBusy[i] || errors.Is(m.locs[i].PointErr, api.ErrNoCoverage) {
		return nil
	}
	m.alertBusy[i] = true
	loc := m.locs[i].Cfg
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, fetchTimeout)
		defer cancel()
		a, err := m.client.ActiveAlerts(ctx, loc.Lat, loc.Lon)
		return alertsMsg{i, a, err}
	}
}

func (m *Model) fetchWeather(i int) tea.Cmd {
	pt := m.locs[i].Point
	if pt == nil || m.wxBusy[i] {
		return nil
	}
	m.wxBusy[i] = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, fetchTimeout)
		defer cancel()
		return wxMsg{i, wx.FetchWeather(ctx, m.client, pt)}
	}
}

func frame() tea.Cmd {
	return tea.Tick(frameTick, func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (m *Model) scheduleAlerts() tea.Cmd {
	gen := m.alertGen
	return tea.Tick(m.cfg.Refresh.Alerts, func(time.Time) tea.Msg { return alertTickMsg{gen} })
}

func (m *Model) scheduleWx() tea.Cmd {
	gen := m.wxGen
	return tea.Tick(m.cfg.Refresh.Forecast, func(time.Time) tea.Msg { return wxTickMsg{gen} })
}

// refreshAll fetches everything now and restarts both refresh intervals.
func (m *Model) refreshAll() tea.Cmd {
	m.alertGen++
	m.wxGen++
	cmds := []tea.Cmd{m.scheduleAlerts(), m.scheduleWx()}
	for i, l := range m.locs {
		cmds = append(cmds, m.fetchAlerts(i))
		if l.Point == nil {
			cmds = append(cmds, m.resolvePoint(i))
		} else {
			cmds = append(cmds, m.fetchWeather(i))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{frame(), m.scheduleAlerts(), m.scheduleWx()}
	for i := range m.locs {
		// Alerts only need lat/lon, so they don't wait on point resolution.
		cmds = append(cmds, m.resolvePoint(i), m.fetchAlerts(i))
	}
	return tea.Batch(cmds...)
}

// ── update ────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutViewport()
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case frameMsg:
		m.now = time.Time(msg)
		m.flash = !m.flash
		m.reclassify() // drops alerts as they expire
		return m, frame()

	case pointMsg:
		m.pointBusy[msg.i] = false
		l := m.locs[msg.i]
		if msg.err != nil {
			l.PointErr = msg.err
			if errors.Is(msg.err, api.ErrNoCoverage) {
				return m, nil // retrying won't help
			}
			return m, tea.Tick(pointRetry, func(time.Time) tea.Msg { return retryPointMsg{msg.i} })
		}
		l.Point, l.PointErr = msg.pt, nil
		return m, m.fetchWeather(msg.i)

	case retryPointMsg:
		if m.locs[msg.i].Point == nil {
			return m, m.resolvePoint(msg.i)
		}
		return m, nil

	case alertsMsg:
		m.alertBusy[msg.i] = false
		m.locs[msg.i].ApplyAlerts(msg.alerts, msg.err, time.Now())
		m.reclassify()
		return m, nil

	case wxMsg:
		m.wxBusy[msg.i] = false
		m.locs[msg.i].ApplyWeather(msg.res, time.Now())
		return m, nil

	case alertTickMsg:
		if msg.gen != m.alertGen {
			return m, nil
		}
		cmds := []tea.Cmd{m.scheduleAlerts()}
		for i := range m.locs {
			cmds = append(cmds, m.fetchAlerts(i))
		}
		return m, tea.Batch(cmds...)

	case wxTickMsg:
		if msg.gen != m.wxGen {
			return m, nil
		}
		cmds := []tea.Cmd{m.scheduleWx()}
		for i := range m.locs {
			cmds = append(cmds, m.fetchWeather(i))
		}
		return m, tea.Batch(cmds...)
	}
	if m.expanded {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

// reclassify recomputes every location's mode, reports transitions to the
// tracker, and rebuilds the merged alert list.
func (m *Model) reclassify() {
	for _, l := range m.locs {
		l.Reclassify(m.now)
		if m.tracker != nil {
			m.tracker.Observe(l.Cfg.Name, l.Mode, l.Cause)
		}
	}
	m.alerts = wx.MergeAlerts(m.locs, m.now)
	if m.alertSel >= len(m.alerts) {
		m.alertSel = max(0, len(m.alerts)-1)
	}
}

func (m *Model) applyDemo() {
	for i, l := range m.locs {
		l.DemoAlerts = demo.Alert(m.demoModes[i], l.Cfg.Name, m.now)
	}
	m.reclassify()
}

// ── keys ──────────────────────────────────────────────────────────────────

func (m *Model) handleKey(k tea.KeyMsg) tea.Cmd {
	key := k.String()
	if key == "ctrl+c" {
		m.cancel()
		return tea.Quit
	}
	if m.help {
		if key == "q" {
			m.cancel()
			return tea.Quit
		}
		m.help = false
		return nil
	}
	if m.expanded {
		switch key {
		case "esc", "enter", "backspace":
			m.expanded = false
		case "q":
			m.cancel()
			return tea.Quit
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(k)
			return cmd
		}
		return nil
	}

	switch key {
	case "q":
		m.cancel()
		return tea.Quit
	case "?":
		m.help = true
	case "r":
		return m.refreshAll()
	case "esc":
		if m.pane == paneAlerts {
			m.pane = paneLocations
		} else {
			m.focus = 0
		}
	case "a":
		if len(m.alerts) > 0 {
			m.pane = paneAlerts
		}
	case "1", "2", "3":
		m.setTab(tab(key[0] - '1'))
	case "tab":
		m.setTab((m.tab + 1) % numTabs)
	case "shift+tab":
		m.setTab((m.tab + numTabs - 1) % numTabs)
	case "v":
		if m.tab == tabMap {
			m.mapLayer = (m.mapLayer + 1) % numLayers
		}
	case "left", "h":
		m.moveH(-1)
	case "right", "l":
		m.moveH(1)
	case "up", "k":
		m.moveV(-1)
	case "down", "j":
		m.moveV(1)
	case "pgup":
		if m.pane == paneAlerts {
			m.alertSel = max(0, m.alertSel-5)
		}
	case "pgdown":
		if m.pane == paneAlerts {
			m.alertSel = min(len(m.alerts)-1, m.alertSel+5)
		}
	case "enter", " ":
		if m.pane == paneAlerts {
			if len(m.alerts) > 0 {
				m.expanded = true
				m.layoutViewport()
				m.vp.SetContent(m.alertDetail(m.alerts[m.alertSel]))
				m.vp.GotoTop()
			}
		} else if m.tab != tabMain {
			// Open the selected location on the Main tab.
			m.focus = m.sel
			m.setTab(tabMain)
		}
	case "m", "M":
		if !m.demo {
			return nil
		}
		cur := m.sel
		if m.tab == tabMain {
			cur = m.focus
		}
		if key == "m" {
			m.demoModes[cur] = (m.demoModes[cur] + 1) % 4
		} else {
			// Cycle every location together, based on the current one.
			next := (m.demoModes[cur] + 1) % 4
			for i := range m.locs {
				m.demoModes[i] = next
			}
		}
		m.applyDemo()
	}
	return nil
}

func (m *Model) setTab(t tab) {
	m.tab, m.pane = t, paneLocations
	if t == tabNearby && m.sel == 0 && len(m.locs) > 1 {
		m.sel = 1 // the primary isn't on the Nearby tab
	}
}

// moveH moves left/right: along grid rows on Nearby, spatially on Map.
func (m *Model) moveH(d int) {
	if m.pane != paneLocations {
		return
	}
	switch m.tab {
	case tabNearby:
		n, cols := len(m.locs), max(1, m.gridCols)
		col := (m.sel - 1) % cols
		switch {
		case d < 0 && col > 0:
			m.sel--
		case d > 0 && col < cols-1 && m.sel+1 < n:
			m.sel++
		}
	case tabMap:
		m.mapNav(d, 0)
	}
}

// moveV moves up/down within a tab, falling into the alerts pane at the
// bottom and back out of it at the top.
func (m *Model) moveV(d int) {
	if m.pane == paneAlerts {
		if d < 0 && m.alertSel == 0 {
			m.pane = paneLocations
			return
		}
		m.alertSel = max(0, min(len(m.alerts)-1, m.alertSel+d))
		return
	}
	moved := false
	switch m.tab {
	case tabNearby:
		n, cols := len(m.locs), max(1, m.gridCols)
		if next := m.sel + d*cols; next >= 1 && next < n {
			m.sel, moved = next, true
		}
	case tabMap:
		moved = m.mapNav(0, d)
	}
	if !moved && d > 0 && len(m.alerts) > 0 {
		m.pane = paneAlerts
	}
}

func (m *Model) layoutViewport() {
	w, h := max(20, m.width-8), max(5, m.height-6)
	if m.vp.Width == 0 {
		m.vp = viewport.New(w, h)
	} else {
		m.vp.Width, m.vp.Height = w, h
	}
	if m.expanded && len(m.alerts) > 0 {
		m.vp.SetContent(m.alertDetail(m.alerts[m.alertSel]))
	}
}

// Shutdown cancels in-flight requests.
func (m *Model) Shutdown() { m.cancel() }

// statusLine summarises locations that are failing to load.
func (m *Model) statusLine() string {
	for _, l := range m.locs {
		if errors.Is(l.PointErr, api.ErrNoCoverage) {
			return fmt.Sprintf("%s is outside NWS coverage (US only); remove it with: ttywx remove %q", l.Cfg.Name, l.Cfg.Name)
		}
		if l.Point == nil && l.PointErr != nil {
			return fmt.Sprintf("%s: location lookup failed (%v), retrying", l.Cfg.Name, l.PointErr)
		}
	}
	return ""
}

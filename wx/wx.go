// Package wx holds per-location weather state and the fetches that fill it.
// Both the TUI and --once use it. Failed fetches never clear previous data;
// they only record the error so the UI can mark the panel stale.
package wx

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/jdankney/ttywx/api"
	"github.com/jdankney/ttywx/config"
	"github.com/jdankney/ttywx/modes"
)

const (
	ForecastPeriods = 6
	HourlyPeriods   = 24
)

type Location struct {
	Cfg config.Location

	Point    *api.Point
	PointErr error

	Obs       *api.Observation
	Forecast  []api.Period
	Hourly    []api.Period
	WxUpdated time.Time // last time any weather fetch fully succeeded
	WxErr     error     // error from the most recent weather fetch, if any

	Alerts        []api.Alert // real alerts from the API
	DemoAlerts    []api.Alert // injected by --demo
	AlertsUpdated time.Time
	AlertsErr     error

	Mode  modes.Mode
	Cause *api.Alert
}

// AllAlerts returns real plus injected alerts that are still live.
func (l *Location) AllAlerts(now time.Time) []api.Alert {
	var out []api.Alert
	for _, set := range [][]api.Alert{l.DemoAlerts, l.Alerts} {
		for _, a := range set {
			if a.Live(now) {
				out = append(out, a)
			}
		}
	}
	return out
}

// Reclassify recomputes Mode/Cause from the current alerts.
func (l *Location) Reclassify(now time.Time) {
	alerts := l.AllAlerts(now)
	m, cause := modes.Worst(alerts, now)
	l.Mode, l.Cause = m, nil
	if cause != nil {
		c := *cause
		l.Cause = &c
	}
}

// Stale reports whether data last refreshed at updated, with lastErr from
// the latest attempt, should be flagged as out of date.
func Stale(updated time.Time, lastErr error, interval time.Duration, now time.Time) bool {
	if updated.IsZero() {
		return false // nothing to be stale; the UI shows loading/error instead
	}
	return lastErr != nil || now.Sub(updated) > 2*interval+30*time.Second
}

// WeatherResult is the outcome of one weather refresh. Each part may fail
// independently.
type WeatherResult struct {
	Obs                      *api.Observation
	Forecast, Hourly         []api.Period
	ObsErr, FcErr, HourlyErr error
}

// Err returns the first error, if any.
func (r WeatherResult) Err() error {
	for _, e := range []error{r.ObsErr, r.FcErr, r.HourlyErr} {
		if e != nil {
			return e
		}
	}
	return nil
}

// FetchWeather gets the latest observation and both forecasts concurrently.
func FetchWeather(ctx context.Context, c *api.Client, p *api.Point) WeatherResult {
	var r WeatherResult
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); r.Obs, r.ObsErr = c.LatestObservation(ctx, p) }()
	go func() { defer wg.Done(); r.Forecast, r.FcErr = c.Forecast(ctx, p.Forecast, ForecastPeriods) }()
	go func() {
		defer wg.Done()
		// Ask for a couple extra hours so we can drop any that already ended.
		var hrs []api.Period
		hrs, r.HourlyErr = c.Forecast(ctx, p.ForecastHourly, HourlyPeriods+3)
		now := time.Now()
		for _, h := range hrs {
			if h.End.After(now) && len(r.Hourly) < HourlyPeriods {
				r.Hourly = append(r.Hourly, h)
			}
		}
	}()
	wg.Wait()
	return r
}

// ApplyWeather merges a result, keeping old values for parts that failed.
func (l *Location) ApplyWeather(r WeatherResult, now time.Time) {
	if r.ObsErr == nil {
		l.Obs = r.Obs
	}
	if r.FcErr == nil {
		l.Forecast = r.Forecast
	}
	if r.HourlyErr == nil {
		l.Hourly = r.Hourly
	}
	l.WxErr = r.Err()
	if l.WxErr == nil {
		l.WxUpdated = now
	} else if l.WxUpdated.IsZero() && (r.ObsErr == nil || r.FcErr == nil) {
		// Partial success on the first load still counts as having data.
		l.WxUpdated = now
	}
}

// ApplyAlerts records an alerts fetch, keeping the previous alerts on error.
func (l *Location) ApplyAlerts(alerts []api.Alert, err error, now time.Time) {
	l.AlertsErr = err
	if err == nil {
		l.Alerts = alerts
		l.AlertsUpdated = now
	}
	l.Reclassify(now)
}

// AlertEntry is one deduplicated alert across all locations.
type AlertEntry struct {
	Alert     api.Alert
	Mode      modes.Mode
	Locations []string
}

// MergeAlerts deduplicates live alerts by ID across locations and sorts
// them worst mode first, then soonest-ending.
func MergeAlerts(locs []*Location, now time.Time) []AlertEntry {
	byID := map[string]*AlertEntry{}
	var order []string
	for _, l := range locs {
		for _, a := range l.AllAlerts(now) {
			e, ok := byID[a.ID]
			if !ok {
				e = &AlertEntry{Alert: a, Mode: modes.Classify(&a)}
				byID[a.ID] = e
				order = append(order, a.ID)
			}
			e.Locations = append(e.Locations, l.Cfg.Name)
		}
	}
	out := make([]AlertEntry, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Mode != out[j].Mode {
			return out[i].Mode > out[j].Mode
		}
		return out[i].Alert.EndTime().Before(out[j].Alert.EndTime())
	})
	return out
}

// Overall returns the worst location (index into locs) and its mode. With
// all locations Normal, it returns index -1.
func Overall(locs []*Location) (modes.Mode, int) {
	best, idx := modes.Normal, -1
	for i, l := range locs {
		if l.Mode > best {
			best, idx = l.Mode, i
		}
	}
	return best, idx
}

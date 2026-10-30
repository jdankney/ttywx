package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/j4mes/ttywx/api"
	"github.com/j4mes/ttywx/config"
	"github.com/j4mes/ttywx/demo"
	"github.com/j4mes/ttywx/modes"
	"github.com/j4mes/ttywx/units"
	"github.com/j4mes/ttywx/wx"
)

// runOnce fetches everything once, prints a plain-text summary and returns
// the process exit code: 0 on success, 1 if no location could be loaded.
func runOnce(cfg *config.Config, client *api.Client, demoMode bool, w io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	locs := make([]*wx.Location, len(cfg.Locations))
	var wg sync.WaitGroup
	for i, lc := range cfg.Locations {
		l := &wx.Location{Cfg: lc}
		locs[i] = l
		wg.Add(1)
		go func() {
			defer wg.Done()
			alerts, err := client.ActiveAlerts(ctx, lc.Lat, lc.Lon)
			now := time.Now()
			l.ApplyAlerts(alerts, err, now)
			l.Point, l.PointErr = client.ResolvePoint(ctx, lc.Lat, lc.Lon)
			if l.PointErr == nil {
				l.ApplyWeather(wx.FetchWeather(ctx, client, l.Point), time.Now())
			}
		}()
	}
	wg.Wait()

	now := time.Now()
	if demoMode {
		for i, m := range demo.Initial(len(locs)) {
			locs[i].DemoAlerts = demo.Alert(m, locs[i].Cfg.Name, now)
		}
	}
	for _, l := range locs {
		l.Reclassify(now)
	}

	u := cfg.Display()
	overall, idx := wx.Overall(locs)
	fmt.Fprintf(w, "ttywx — %s\n", now.Format(u.Layout("Mon Jan 2 2006 {clock} MST")))
	if idx >= 0 {
		cause := ""
		if c := locs[idx].Cause; c != nil {
			cause = " (" + c.Event + ")"
		}
		fmt.Fprintf(w, "Overall: %s — %s%s\n", overall, locs[idx].Cfg.Name, cause)
	} else {
		fmt.Fprintf(w, "Overall: %s\n", overall)
	}
	if demoMode {
		fmt.Fprintln(w, "*** DEMO MODE: alerts below include simulated ones ***")
	}

	loaded := 0
	fmt.Fprintln(w)
	for _, l := range locs {
		if l.Obs != nil || !l.AlertsUpdated.IsZero() {
			loaded++
		}
		fmt.Fprintf(w, "%-14s %-19s %s\n", l.Cfg.Name, "["+l.Mode.String()+"]", summary(u, l))
		if names := eventNames(l.AllAlerts(now)); names != "" {
			fmt.Fprintf(w, "%-14s %-19s alerts: %s\n", "", "", names)
		}
		seen := map[string]bool{} // an out-of-coverage point fails every fetch the same way
		for _, err := range []error{l.PointErr, l.WxErr, l.AlertsErr} {
			if err != nil && !seen[err.Error()] {
				seen[err.Error()] = true
				fmt.Fprintf(w, "%-14s %-19s error: %v\n", "", "", err)
			}
		}
	}

	p := locs[0]
	if len(p.Forecast) > 0 {
		fmt.Fprintf(w, "\n%s forecast\n", p.Cfg.Name)
		for _, f := range p.Forecast {
			pop := ""
			if f.PrecipChance != nil && *f.PrecipChance > 0 {
				pop = fmt.Sprintf(" (%.0f%% precip)", *f.PrecipChance)
			}
			fmt.Fprintf(w, "  %-16s %3d°%s  %s%s\n", f.Name, u.DegInt(f.Temperature), u.TempSymbol(), f.Short, pop)
		}
	}
	if len(p.Hourly) > 0 {
		hrs := p.Hourly[:min(12, len(p.Hourly))]
		var parts []string
		for _, h := range hrs {
			label := u.Hour(h.Start.Local())
			if u.Clock24 {
				label = h.Start.Local().Format("15:04") // a bare "15" is ambiguous in prose
			}
			parts = append(parts, fmt.Sprintf("%s %d°", label, u.DegInt(h.Temperature)))
		}
		fmt.Fprintf(w, "\nNext %d hours: %s\n", len(hrs), strings.Join(parts, " · "))
	}

	alerts := wx.MergeAlerts(locs, now)
	if len(alerts) > 0 {
		fmt.Fprintf(w, "\nActive alerts (%d)\n", len(alerts))
		for _, e := range alerts {
			end := "—"
			if t := e.Alert.EndTime(); !t.IsZero() {
				end = t.Local().Format(u.Layout("Mon {clock}"))
			}
			fmt.Fprintf(w, "  [%s] %s — until %s — %s\n", e.Mode, e.Alert.Event, end, strings.Join(e.Locations, ", "))
			if e.Alert.Headline != "" {
				fmt.Fprintf(w, "      %s\n", e.Alert.Headline)
			}
		}
	} else {
		fmt.Fprintln(w, "\nNo active alerts.")
	}

	if loaded == 0 {
		return 1
	}
	return 0
}

func summary(u units.Units, l *wx.Location) string {
	o := l.Obs
	if o == nil {
		return "—"
	}
	f := func(v *float64, format func(float64) string) string {
		if v == nil {
			return "—"
		}
		return format(*v)
	}
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v) }
	sky := o.Description
	if sky == "" && len(l.Hourly) > 0 {
		sky = l.Hourly[0].Short
	}
	if sky == "" {
		sky = "—"
	}
	wind := "—"
	if o.WindMph != nil && *o.WindMph < 0.5 {
		wind = "calm"
	} else if o.WindMph != nil {
		wind = u.Speed(*o.WindMph)
		if o.WindDirDeg != nil {
			wind = api.Compass(*o.WindDirDeg) + " " + wind
		}
	}
	return fmt.Sprintf("%s (feels %s)  %s RH  wind %s  %s",
		f(o.TempF, u.Temp), f(o.FeelsLikeF, u.Temp), f(o.Humidity, pct), wind, sky)
}

func eventNames(alerts []api.Alert) string {
	seen := map[string]bool{}
	var out []string
	for _, a := range alerts {
		if !seen[a.Event] {
			seen[a.Event] = true
			out = append(out, fmt.Sprintf("%s [%s]", a.Event, modes.Classify(&a).Short()))
		}
	}
	return strings.Join(out, ", ")
}

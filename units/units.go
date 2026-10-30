// Package units formats weather values and times for display. Values are
// stored internally in US units (°F, mph, inHg, miles, inches), the way the
// NWS forecast endpoints report them; Units converts at the last moment.
package units

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type Units struct {
	Metric  bool // °C, km/h, hPa, km, mm instead of °F, mph, inHg, mi, in
	Clock24 bool // 15:04 instead of 3:04 PM
}

// ── temperature ───────────────────────────────────────────────────────────

// TempValue converts a Fahrenheit temperature to the display unit.
func (u Units) TempValue(f float64) float64 {
	if u.Metric {
		return (f - 32) * 5 / 9
	}
	return f
}

// TempSymbol is "F" or "C".
func (u Units) TempSymbol() string {
	if u.Metric {
		return "C"
	}
	return "F"
}

// Deg is a rounded temperature with a bare degree sign: "72°".
func (u Units) Deg(f float64) string {
	return fmt.Sprintf("%.0f°", noNegZero(u.TempValue(f)))
}

// Temp is a rounded temperature with its unit: "72°F" / "22°C".
func (u Units) Temp(f float64) string {
	return u.Deg(f) + u.TempSymbol()
}

// DegInt converts a whole-degree Fahrenheit forecast temperature, as the
// forecast endpoints return them, to a whole number in the display unit.
func (u Units) DegInt(f int) int {
	return int(noNegZero(math.Round(u.TempValue(float64(f)))))
}

// ── other quantities ──────────────────────────────────────────────────────

// SpeedValue converts mph to the display unit.
func (u Units) SpeedValue(mph float64) float64 {
	if u.Metric {
		return mph * 1.609344
	}
	return mph
}

// SpeedUnit is "mph" or "km/h".
func (u Units) SpeedUnit() string {
	if u.Metric {
		return "km/h"
	}
	return "mph"
}

// Speed is "12 mph" / "19 km/h".
func (u Units) Speed(mph float64) string {
	return fmt.Sprintf("%.0f %s", u.SpeedValue(mph), u.SpeedUnit())
}

// Pressure is "30.01 inHg" / "1016 hPa".
func (u Units) Pressure(inHg float64) string {
	if u.Metric {
		return fmt.Sprintf("%.0f hPa", inHg*33.86389)
	}
	return fmt.Sprintf("%.2f inHg", inHg)
}

// Distance is "10 mi" / "16 km".
func (u Units) Distance(mi float64) string {
	if u.Metric {
		return fmt.Sprintf("%.0f km", mi*1.609344)
	}
	return fmt.Sprintf("%.0f mi", mi)
}

// DistanceUnit is "mi" or "km"; KmPer is how many km one of them spans.
func (u Units) DistanceUnit() (name string, kmPer float64) {
	if u.Metric {
		return "km", 1
	}
	return "mi", 1.609344
}

// Precip is "0.10 in" / "2.5 mm".
func (u Units) Precip(in float64) string {
	if u.Metric {
		return fmt.Sprintf("%.1f mm", in*25.4)
	}
	return fmt.Sprintf("%.2f in", in)
}

// noNegZero turns values that round to -0 into 0 so they don't print as "-0".
func noNegZero(v float64) float64 {
	if math.Round(v) == 0 {
		return 0
	}
	return v
}

// ── time ──────────────────────────────────────────────────────────────────

// Clock is "3:04 PM" / "15:04" in local time, or "—" for the zero time.
func (u Units) Clock(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format(u.clockLayout())
}

// Layout returns a time layout with "{clock}" replaced by the hour:minute
// layout for this setting, and "{clocksec}" by the same with seconds. For
// example Layout("Mon {clock}") is "Mon 3:04 PM" or "Mon 15:04".
func (u Units) Layout(s string) string {
	sec := "3:04:05 PM"
	if u.Clock24 {
		sec = "15:04:05"
	}
	return strings.NewReplacer("{clocksec}", sec, "{clock}", u.clockLayout()).Replace(s)
}

func (u Units) clockLayout() string {
	if u.Clock24 {
		return "15:04"
	}
	return "3:04 PM"
}

// Hour is a compact hour label for hourly charts: "3p" / "15".
func (u Units) Hour(t time.Time) string {
	h := t.Hour()
	if u.Clock24 {
		return fmt.Sprintf("%02d", h)
	}
	suffix := "a"
	if h >= 12 {
		suffix = "p"
	}
	h %= 12
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d%s", h, suffix)
}

package units

import (
	"testing"
	"time"
)

func TestTemp(t *testing.T) {
	imp, met := Units{}, Units{Metric: true}
	cases := []struct {
		u    Units
		f    float64
		want string
	}{
		{imp, 72.4, "72°F"},
		{met, 72, "22°C"},
		{met, 32, "0°C"},
		{met, 31.5, "0°C"}, // -0.28 would print as "-0"
		{met, -40, "-40°C"},
	}
	for _, c := range cases {
		if got := c.u.Temp(c.f); got != c.want {
			t.Errorf("Temp(%v, metric=%v) = %q, want %q", c.f, c.u.Metric, got, c.want)
		}
	}
	if got := met.DegInt(50); got != 10 {
		t.Errorf("DegInt(50) = %d, want 10", got)
	}
}

func TestQuantities(t *testing.T) {
	met := Units{Metric: true}
	for _, c := range []struct{ got, want string }{
		{Units{}.Speed(10), "10 mph"},
		{met.Speed(10), "16 km/h"},
		{met.Pressure(30), "1016 hPa"},
		{Units{}.Pressure(30), "30.00 inHg"},
		{met.Distance(10), "16 km"},
		{met.Precip(0.1), "2.5 mm"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestClock(t *testing.T) {
	tm := time.Date(2026, 10, 30, 15, 4, 5, 0, time.Local)
	if got := (Units{}).Clock(tm); got != "3:04 PM" {
		t.Errorf("12h clock = %q", got)
	}
	if got := (Units{Clock24: true}).Clock(tm); got != "15:04" {
		t.Errorf("24h clock = %q", got)
	}
	if got := tm.Format((Units{Clock24: true}).Layout("Mon {clocksec}")); got != "Fri 15:04:05" {
		t.Errorf("Layout = %q", got)
	}
	if got := (Units{}).Hour(tm); got != "3p" {
		t.Errorf("12h hour = %q", got)
	}
	if got := (Units{Clock24: true}).Hour(tm); got != "15" {
		t.Errorf("24h hour = %q", got)
	}
}

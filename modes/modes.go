// Package modes turns NWS alerts into one of four threat modes.
package modes

import (
	"strings"
	"time"

	"github.com/j4mes/ttywx/api"
)

type Mode int

const (
	Normal Mode = iota
	Warning
	Heightened
	TakeCover
)

func (m Mode) String() string {
	switch m {
	case Warning:
		return "WARNING"
	case Heightened:
		return "HEIGHTENED WARNING"
	case TakeCover:
		return "TAKE COVER"
	}
	return "NORMAL"
}

// Short is a compact label for tight spaces.
func (m Mode) Short() string {
	switch m {
	case Warning:
		return "WARN"
	case Heightened:
		return "HEIGHTENED"
	case TakeCover:
		return "TAKE COVER"
	}
	return "NORMAL"
}

var eventIndex = func() map[string]Mode {
	idx := make(map[string]Mode, len(EventModes))
	for k, v := range EventModes {
		idx[strings.ToLower(k)] = v
	}
	return idx
}()

// Classify returns the threat mode for a single alert.
func Classify(a *api.Alert) Mode {
	event := strings.ToLower(strings.TrimSpace(a.Event))
	m, known := eventIndex[event]
	if !known {
		m = fallback(event, a.Severity)
	}
	// Life-safety overrides apply even to listed events.
	if strings.EqualFold(a.Severity, "Extreme") && strings.EqualFold(a.Urgency, "Immediate") {
		m = TakeCover
	}
	if catastrophic(a) {
		m = TakeCover
	}
	return m
}

// fallback handles events missing from EventModes: first by the event's
// wording, then by severity.
func fallback(event, severity string) Mode {
	if strings.EqualFold(severity, "Severe") && !strings.Contains(event, "emergency") {
		return Heightened
	}
	switch {
	case strings.Contains(event, "emergency"):
		return TakeCover
	case strings.Contains(event, "warning"):
		return Heightened
	case strings.Contains(event, "watch"), strings.Contains(event, "advisory"):
		return Warning
	case strings.Contains(event, "statement"), strings.Contains(event, "outlook"):
		return Normal
	}
	switch strings.ToLower(severity) {
	case "minor", "moderate":
		return Warning
	}
	return Normal
}

func catastrophic(a *api.Alert) bool {
	for _, p := range []string{"flashFloodDamageThreat", "tornadoDamageThreat", "thunderstormDamageThreat"} {
		if strings.EqualFold(a.Param(p), "catastrophic") {
			return true
		}
	}
	// Belt and braces: emergencies are announced in the NWS headline.
	h := strings.ToUpper(a.Param("NWSheadline"))
	return strings.Contains(h, "FLASH FLOOD EMERGENCY") || strings.Contains(h, "TORNADO EMERGENCY")
}

// Worst returns the highest mode among the live alerts and the alert that
// caused it (nil when there are no live alerts). Ties go to the most
// recently sent alert, which is usually the most relevant update.
func Worst(alerts []api.Alert, now time.Time) (Mode, *api.Alert) {
	best, idx := Normal, -1
	for i := range alerts {
		a := &alerts[i]
		if !a.Live(now) {
			continue
		}
		m := Classify(a)
		if idx < 0 || m > best || (m == best && a.Sent.After(alerts[idx].Sent)) {
			best, idx = m, i
		}
	}
	if idx < 0 {
		return Normal, nil
	}
	return best, &alerts[idx]
}

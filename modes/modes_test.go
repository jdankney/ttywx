package modes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/j4mes/ttywx/api"
)

var now = time.Date(2026, 10, 10, 13, 0, 0, 0, time.UTC)

func loadAlerts(t *testing.T, name string) []api.Alert {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var c api.AlertCollection
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return c.Alerts()
}

func TestWorstFromSampleJSON(t *testing.T) {
	tests := []struct {
		file      string
		want      Mode
		wantEvent string // "" means no causing alert
	}{
		{"none.json", Normal, ""},
		{"statements.json", Normal, "Special Weather Statement"},
		{"advisory.json", Warning, "Wind Advisory"},
		{"red_flag.json", Heightened, "Red Flag Warning"},
		{"tornado.json", TakeCover, "Tornado Warning"},
		{"flash_flood_emergency.json", TakeCover, "Flash Flood Warning"},
		// Test messages, cancellations and ended alerts are ignored.
		{"inactive.json", Warning, "Dense Fog Advisory"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, a := Worst(loadAlerts(t, tt.file), now)
			if got != tt.want {
				t.Errorf("mode = %v, want %v", got, tt.want)
			}
			event := ""
			if a != nil {
				event = a.Event
			}
			if tt.want != Normal && event != tt.wantEvent {
				t.Errorf("cause = %q, want %q", event, tt.wantEvent)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name  string
		alert api.Alert
		want  Mode
	}{
		// Table lookups.
		{"statement", api.Alert{Event: "Special Weather Statement", Severity: "Moderate"}, Normal},
		{"outlook", api.Alert{Event: "Hazardous Weather Outlook", Severity: "Minor"}, Normal},
		{"advisory", api.Alert{Event: "Heat Advisory", Severity: "Moderate"}, Warning},
		{"watch ignores severity", api.Alert{Event: "Flood Watch", Severity: "Severe"}, Warning},
		{"tropical storm watch", api.Alert{Event: "Tropical Storm Watch", Severity: "Severe"}, Warning},
		{"warning not escalated by severity", api.Alert{Event: "Red Flag Warning", Severity: "Severe"}, Heightened},
		{"warning", api.Alert{Event: "High Wind Warning", Severity: "Moderate"}, Heightened},
		{"take cover", api.Alert{Event: "Tsunami Warning", Severity: "Severe"}, TakeCover},
		{"case insensitive", api.Alert{Event: "red flag WARNING"}, Heightened},
		{"civil", api.Alert{Event: "Evacuation Immediate", Severity: "Unknown"}, TakeCover},

		// Unknown events fall back to wording, then severity.
		{"unknown warning", api.Alert{Event: "Glitter Storm Warning"}, Heightened},
		{"unknown watch", api.Alert{Event: "Glitter Storm Watch"}, Warning},
		{"unknown emergency", api.Alert{Event: "Glitter Emergency"}, TakeCover},
		{"unknown minor", api.Alert{Event: "Glitter Notice", Severity: "Minor"}, Warning},
		{"unknown moderate", api.Alert{Event: "Glitter Notice", Severity: "Moderate"}, Warning},
		{"unknown severe", api.Alert{Event: "Glitter Notice", Severity: "Severe"}, Heightened},
		{"unknown unknown", api.Alert{Event: "Glitter Notice", Severity: "Unknown"}, Normal},

		// Extreme + Immediate is always TAKE COVER; Extreme alone isn't.
		{"extreme immediate", api.Alert{Event: "Glitter Notice", Severity: "Extreme", Urgency: "Immediate"}, TakeCover},
		{"extreme expected", api.Alert{Event: "Excessive Heat Warning", Severity: "Extreme", Urgency: "Expected"}, Heightened},

		// Emergencies are tagged via parameters on ordinary warnings.
		{"tornado emergency", api.Alert{Event: "Tornado Warning", Parameters: map[string][]string{"tornadoDamageThreat": {"CATASTROPHIC"}}}, TakeCover},
		{"ffw considerable", api.Alert{Event: "Flash Flood Warning", Parameters: map[string][]string{"flashFloodDamageThreat": {"Considerable"}}}, Heightened},
		{"ffe headline", api.Alert{Event: "Flash Flood Warning", Parameters: map[string][]string{"NWSheadline": {"FLASH FLOOD EMERGENCY FOR RAMONA"}}}, TakeCover},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(&tt.alert); got != tt.want {
				t.Errorf("Classify(%q, %q) = %v, want %v", tt.alert.Event, tt.alert.Severity, got, tt.want)
			}
		})
	}
}

func TestWorstTieBreaksOnNewest(t *testing.T) {
	older := api.Alert{ID: "a", Event: "Wind Advisory", Sent: now.Add(-2 * time.Hour)}
	newer := api.Alert{ID: "b", Event: "Heat Advisory", Sent: now.Add(-time.Hour)}
	m, a := Worst([]api.Alert{older, newer}, now)
	if m != Warning || a.ID != "b" {
		t.Errorf("got %v %q, want WARNING from newest alert b", m, a.ID)
	}
}

func TestEventTableHasNoCaseDuplicates(t *testing.T) {
	if len(eventIndex) != len(EventModes) {
		t.Errorf("EventModes has entries differing only by case")
	}
}

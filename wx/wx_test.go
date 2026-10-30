package wx

import (
	"errors"
	"testing"
	"time"

	"github.com/j4mes/ttywx/api"
	"github.com/j4mes/ttywx/modes"
)

func TestFailedFetchKeepsLastGoodData(t *testing.T) {
	now := time.Now()
	l := &Location{}
	l.ApplyAlerts([]api.Alert{{ID: "1", Event: "Red Flag Warning"}}, nil, now)
	if l.Mode != modes.Heightened {
		t.Fatalf("mode = %v", l.Mode)
	}

	later := now.Add(time.Minute)
	l.ApplyAlerts(nil, errors.New("HTTP 503"), later)
	if len(l.Alerts) != 1 || l.Mode != modes.Heightened {
		t.Errorf("alerts were dropped on error: %+v mode %v", l.Alerts, l.Mode)
	}
	if !l.AlertsUpdated.Equal(now) {
		t.Errorf("AlertsUpdated advanced on failure")
	}
	if !Stale(l.AlertsUpdated, l.AlertsErr, time.Minute, later) {
		t.Errorf("expected stale after failed refresh")
	}

	temp := 70.0
	l.ApplyWeather(WeatherResult{Obs: &api.Observation{TempF: &temp}, Forecast: []api.Period{{Name: "Tonight"}}}, now)
	l.ApplyWeather(WeatherResult{ObsErr: errors.New("timeout"), Forecast: []api.Period{{Name: "Sunday"}}}, later)
	if l.Obs == nil || *l.Obs.TempF != 70 {
		t.Errorf("observation dropped on error")
	}
	if l.Forecast[0].Name != "Sunday" {
		t.Errorf("successful part of a partial refresh not applied")
	}
	if !Stale(l.WxUpdated, l.WxErr, 10*time.Minute, later) {
		t.Errorf("expected weather stale after partial failure")
	}
}

func TestStaleByAge(t *testing.T) {
	now := time.Now()
	if Stale(now.Add(-time.Minute), nil, time.Minute, now) {
		t.Error("fresh data marked stale")
	}
	if !Stale(now.Add(-5*time.Minute), nil, time.Minute, now) {
		t.Error("old data not marked stale")
	}
	if Stale(time.Time{}, errors.New("x"), time.Minute, now) {
		t.Error("never-loaded data should show loading/error, not stale")
	}
}

func TestMergeAlertsDedupesAndSorts(t *testing.T) {
	now := time.Now()
	a := &Location{Alerts: []api.Alert{{ID: "adv", Event: "Wind Advisory"}, {ID: "rfw", Event: "Red Flag Warning"}}}
	a.Cfg.Name = "Escondido"
	b := &Location{Alerts: []api.Alert{{ID: "rfw", Event: "Red Flag Warning"}}}
	b.Cfg.Name = "Ramona"
	got := MergeAlerts([]*Location{a, b}, now)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Alert.ID != "rfw" || len(got[0].Locations) != 2 {
		t.Errorf("first = %+v, want rfw shared by 2 locations", got[0])
	}
}

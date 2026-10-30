package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(url string) *Client {
	c := NewClient("test@example.com")
	c.BaseURL = url
	c.RetryBase = time.Millisecond
	return c
}

func TestRetriesOn503ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "ttywx/"+Version+" (contact: test@example.com)" {
			t.Errorf("User-Agent = %q", ua)
		}
		if acc := r.Header.Get("Accept"); acc != "application/geo+json" {
			t.Errorf("Accept = %q", acc)
		}
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"features":[{"properties":{"id":"x","event":"Wind Advisory"}}]}`))
	}))
	defer srv.Close()

	alerts, err := testClient(srv.URL).ActiveAlerts(context.Background(), 33.1192, -117.0864)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Event != "Wind Advisory" {
		t.Errorf("alerts = %+v", alerts)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("calls = %d, want 3", n)
	}
}

func TestNoRetryOn404AndProblemDetail(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"title":"Not Found","detail":"No observations"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).observation(context.Background(), "KXXX")
	he, ok := err.(*HTTPError)
	if !ok || he.Status != 404 || he.Detail != "No observations" {
		t.Fatalf("err = %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	if _, err := c.ActiveAlerts(context.Background(), 0, 0); err == nil {
		t.Fatal("expected error")
	}
	if n := calls.Load(); n != int32(c.Retries+1) {
		t.Errorf("calls = %d, want %d", n, c.Retries+1)
	}
}

func TestUnitConversions(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	near := func(got *float64, want float64) bool { return got != nil && *got > want-0.01 && *got < want+0.01 }
	if g := tempF(quantity{"wmoUnit:degC", f(22.22)}); !near(g, 72.0) {
		t.Errorf("tempF = %v", g)
	}
	if g := speedMph(quantity{"wmoUnit:km_h-1", f(16.0934)}); !near(g, 10) {
		t.Errorf("speedMph = %v", g)
	}
	if g := lengthIn(quantity{"wmoUnit:mm", f(25.4)}); !near(g, 1) {
		t.Errorf("lengthIn = %v", g)
	}
	if g := tempF(quantity{"wmoUnit:degC", nil}); g != nil {
		t.Errorf("null value should stay nil, got %v", *g)
	}
	if g := tempF(quantity{"wmoUnit:furlongs", f(1)}); g != nil {
		t.Errorf("unknown unit should be nil")
	}
	if Compass(294) != "WNW" || Compass(359) != "N" || Compass(0) != "N" {
		t.Errorf("Compass wrong: %s %s", Compass(294), Compass(359))
	}
}

func TestNoCoverage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alerts/active" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"detail":"Parameter \"point\" is invalid: out of bounds"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"detail":"Unable to provide data for requested point 51.5,-0.12"}`))
	}))
	defer srv.Close()
	c := testClient(srv.URL)

	if _, err := c.ResolvePoint(context.Background(), 51.5, -0.12); !errors.Is(err, ErrNoCoverage) {
		t.Errorf("ResolvePoint err = %v, want ErrNoCoverage", err)
	}
	if _, err := c.ActiveAlerts(context.Background(), 51.5, -0.12); !errors.Is(err, ErrNoCoverage) {
		t.Errorf("ActiveAlerts err = %v, want ErrNoCoverage", err)
	}
}

func TestForecastSI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("units") != "si" {
			t.Errorf("units = %q, want si", r.URL.Query().Get("units"))
		}
		w.Write([]byte(`{"properties":{"periods":[{"name":"Today","temperature":19,"temperatureUnit":"C","detailedForecast":"High near 19."}]}}`))
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	c.SI = true
	ps, err := c.Forecast(context.Background(), srv.URL+"/gridpoints/BOU/1,1/forecast", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Temperature != 66 || ps[0].TempUnit != "F" || ps[0].Detailed != "High near 19." {
		t.Errorf("period = %+v, want 66 F with metric prose", ps[0])
	}
}

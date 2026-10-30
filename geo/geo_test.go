package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sample = `{"results":[
 {"name":"Springfield","latitude":39.80,"longitude":-89.64,"country_code":"US","admin1":"Illinois","admin2":"Sangamon","population":114394},
 {"name":"Springfield","latitude":37.21,"longitude":-93.29,"country_code":"US","admin1":"Missouri","admin2":"Greene","population":169176},
 {"name":"Springfield","latitude":-27.68,"longitude":152.91,"country_code":"AU","admin1":"Queensland"},
 {"name":"San Juan","latitude":18.47,"longitude":-66.11,"country_code":"PR","admin1":"San Juan"}
]}`

func testClient(t *testing.T) (*Client, *string) {
	var gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("name")
		w.Write([]byte(sample))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("ttywx-test")
	c.BaseURL = srv.URL
	return c, &gotName
}

func TestSearchFiltersToCoverage(t *testing.T) {
	c, _ := testClient(t)
	got, err := c.Search(context.Background(), "Springfield")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 US/territory results (no Australia), got %+v", got)
	}
	if got[2].StateCode != "PR" {
		t.Errorf("territory code = %q, want PR", got[2].StateCode)
	}
}

func TestSearchState(t *testing.T) {
	c, name := testClient(t)
	for _, q := range []string{"Springfield, MO", "springfield, missouri", "Springfield, MO, USA"} {
		got, err := c.Search(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(*name, "Springfield") {
			t.Errorf("%q: searched for %q", q, *name)
		}
		if len(got) != 1 || got[0].State != "Missouri" {
			t.Errorf("%q: got %+v", q, got)
		}
		if l := got[0].Label(); l != "Springfield, MO (Greene)" {
			t.Errorf("label = %q", l)
		}
	}
}

func TestSearchZip(t *testing.T) {
	c, name := testClient(t)
	if _, err := c.Search(context.Background(), "92025-1234"); err != nil {
		t.Fatal(err)
	}
	if *name != "92025" {
		t.Errorf("ZIP+4 searched as %q", *name)
	}
}

func TestParseCoords(t *testing.T) {
	if lat, lon, ok := ParseCoords(" 39.7392, -104.9847 "); !ok || lat != 39.7392 || lon != -104.9847 {
		t.Errorf("got %v %v %v", lat, lon, ok)
	}
	for _, s := range []string{"Denver, CO", "91,0", "1"} {
		if _, _, ok := ParseCoords(s); ok {
			t.Errorf("%q parsed as coordinates", s)
		}
	}
}

func TestSearchPrefersTowns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
 {"name":"Denver","latitude":39.7,"longitude":-104.9,"country_code":"US","admin1":"Colorado","feature_code":"PPLA"},
 {"name":"Denver International Airport","latitude":39.8,"longitude":-104.6,"country_code":"US","admin1":"Colorado","feature_code":"AIRP"},
 {"name":"Denver City","latitude":32.9,"longitude":-102.8,"country_code":"US","admin1":"Texas","feature_code":"PPL"},
 {"name":"Denver","latitude":40.2,"longitude":-76.1,"country_code":"US","admin1":"Pennsylvania","feature_code":"PPL"}
]}`))
	}))
	defer srv.Close()
	c := NewClient("ttywx-test")
	c.BaseURL = srv.URL
	got, err := c.Search(context.Background(), "Denver")
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, p := range got {
		labels = append(labels, p.Label())
	}
	if s := strings.Join(labels, "; "); s != "Denver, CO; Denver, PA; Denver City, TX" {
		t.Errorf("got %s", s)
	}
}

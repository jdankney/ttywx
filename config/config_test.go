package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTemplateParses(t *testing.T) {
	data := Template("", []Location{{Name: "Denver", Lat: 39.7392, Lon: -104.9847, Primary: true}})
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("template does not parse: %v\n%s", err, data)
	}
	if cfg.Contact != PlaceholderContact || len(cfg.Locations) != 1 || cfg.Locations[0].Name != "Denver" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if d := cfg.Display(); d.Metric || d.Clock24 {
		t.Errorf("defaults should be imperial / 12h, got %+v", d)
	}
}

func TestTemplateQuotesAwkwardNames(t *testing.T) {
	data := Template("me: you@example.com", []Location{{Name: "St. Mary's, AK #2", Lat: 62, Lon: -163}})
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if cfg.Contact != "me: you@example.com" || cfg.Locations[0].Name != "St. Mary's, AK #2" {
		t.Errorf("round trip lost data: %+v", cfg)
	}
}

func TestParseValidation(t *testing.T) {
	for name, y := range map[string]string{
		"no locations": "contact: x\n",
		"bad units":    "units: kelvin\nlocations: [{name: A, lat: 1, lon: 1}]\n",
		"bad clock":    "clock: 13h\nlocations: [{name: A, lat: 1, lon: 1}]\n",
		"dup names":    "locations: [{name: A, lat: 1, lon: 1}, {name: a, lat: 2, lon: 2}]\n",
		"bad coords":   "locations: [{name: A, lat: 91, lon: 1}]\n",
	} {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParsePrimaryFirst(t *testing.T) {
	cfg, err := Parse([]byte("locations: [{name: A, lat: 1, lon: 1}, {name: B, lat: 2, lon: 2, primary: true}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Locations[0].Name != "B" || !cfg.Locations[0].Primary || cfg.Locations[1].Primary {
		t.Errorf("primary not moved first: %+v", cfg.Locations)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want ErrNotExist, got %v", err)
	}
}

func TestAddRemoveKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Write(path, Template("me@example.com", []Location{{Name: "Denver", Lat: 39.7392, Lon: -104.9847, Primary: true}})); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("x")); err == nil {
		t.Error("Write replaced an existing file")
	}

	if err := AddLocation(path, Location{Name: "Boulder", Lat: 40.015, Lon: -105.2705}); err != nil {
		t.Fatal(err)
	}
	if err := AddLocation(path, Location{Name: "boulder", Lat: 1, Lon: 1}); err == nil {
		t.Error("duplicate name accepted")
	}
	if err := AddLocation(path, Location{Name: "Aurora", Lat: 39.7294, Lon: -104.8319, Primary: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range cfg.Locations {
		names = append(names, l.Name)
	}
	if got := strings.Join(names, ","); got != "Aurora,Denver,Boulder" {
		t.Errorf("locations = %s, want Aurora (new primary) first", got)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# api.weather.gov requires a User-Agent") {
		t.Errorf("comments were lost:\n%s", data)
	}

	if err := RemoveLocation(path, "DENVER"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLocation(path, "Nowhere"); err == nil {
		t.Error("removing a missing location succeeded")
	}
	if err := RemoveLocation(path, "Aurora"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLocation(path, "Boulder"); err == nil {
		t.Error("removed the last location")
	}
}

func TestEditsTouchOnlyChangedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "contact: me@example.com   # aligned comment\n\n" +
		"locations:\n" +
		"  - { name: A, lat: 1.0000, lon: 1.0000, primary: true }\n" +
		"  # B is a block mapping\n" +
		"  - name: B\n" +
		"    lat: 2\n" +
		"    lon: 2\n" +
		"\n" +
		"bell: false\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AddLocation(path, Location{Name: "C", Lat: 3, Lon: 3, Primary: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "contact: me@example.com   # aligned comment\n\n" +
		"locations:\n" +
		"  - { name: A, lat: 1.0000, lon: 1.0000 }\n" +
		"  # B is a block mapping\n" +
		"  - name: B\n" +
		"    lat: 2\n" +
		"    lon: 2\n" +
		"  - { name: C, lat: 3.0000, lon: 3.0000, primary: true }\n" +
		"\n" +
		"bell: false\n"
	if string(got) != want {
		t.Fatalf("after add:\n%s\nwant:\n%s", got, want)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("permissions changed to %v", info.Mode().Perm())
	}

	if err := RemoveLocation(path, "b"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	want = "contact: me@example.com   # aligned comment\n\n" +
		"locations:\n" +
		"  - { name: A, lat: 1.0000, lon: 1.0000 }\n" +
		"  - { name: C, lat: 3.0000, lon: 3.0000, primary: true }\n" +
		"\n" +
		"bell: false\n"
	if string(got) != want {
		t.Fatalf("after remove:\n%s\nwant:\n%s", got, want)
	}
}

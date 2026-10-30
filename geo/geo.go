// Package geo turns place names and ZIP codes into coordinates using the
// Open-Meteo geocoding API (free, no key). Results are limited to the
// places api.weather.gov covers: the US and its territories.
package geo

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://geocoding-api.open-meteo.com/v1/search"

// MaxResults is how many matches Search returns at most.
const MaxResults = 10

type Place struct {
	Name       string
	State      string // full name, e.g. "Colorado"
	StateCode  string // e.g. "CO"
	County     string
	Lat, Lon   float64
	Population int

	populated bool // a city or town rather than a park, airport, etc.
}

// Label is a human-readable description: "Denver, PA (Lancaster)".
func (p Place) Label() string {
	s := p.Name
	if p.StateCode != "" {
		s += ", " + p.StateCode
	} else if p.State != "" {
		s += ", " + p.State
	}
	if p.County != "" && p.County != p.Name {
		s += " (" + p.County + ")"
	}
	return s
}

type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
}

func NewClient(userAgent string) *Client {
	return &Client{BaseURL: DefaultBaseURL, UserAgent: userAgent, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

var zipRe = regexp.MustCompile(`^(\d{5})(-\d{4})?$`)

// Search finds US places matching query: a city name ("Denver"), a city
// with a state ("Springfield, IL" or "Springfield, Illinois"), or a ZIP
// code. Results are ordered by the API's relevance (roughly population).
func (c *Client) Search(ctx context.Context, query string) ([]Place, error) {
	name, qualifier := splitQuery(query)
	if m := zipRe.FindStringSubmatch(name); m != nil {
		name = m[1]
	}
	if len(name) < 2 {
		return nil, fmt.Errorf("%q is too short to search for", query)
	}
	q := url.Values{"name": {name}, "count": {"100"}, "language": {"en"}, "format": {"json"}}
	var results []result
	var err error
	for attempt := 0; attempt < 2; attempt++ { // one retry for transient failures
		if results, err = c.fetch(ctx, c.BaseURL+"?"+q.Encode()); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("place lookup: %w", err)
	}

	var out []Place
	populated := false
	for _, r := range results {
		code, ok := stateCode(r.CountryCode, r.Admin1)
		if !ok {
			continue // outside NWS coverage
		}
		p := Place{
			Name: r.Name, State: r.Admin1, StateCode: code, County: r.Admin2,
			Lat: r.Latitude, Lon: r.Longitude, Population: r.Population,
			populated: strings.HasPrefix(r.FeatureCode, "PPL"),
		}
		if qualifier == "" || matchesState(p, qualifier) {
			out = append(out, p)
			populated = populated || p.populated
		}
	}
	// The search is fuzzy and includes parks, airports and the like
	// ("Denver ARTCC Heliport"). Prefer populated places (GeoNames PPL*
	// codes) and, among those, exact name matches.
	if populated {
		out = slices.DeleteFunc(out, func(p Place) bool { return !p.populated })
	}
	slices.SortStableFunc(out, func(a, b Place) int {
		return cmp.Compare(rank(a.Name, name), rank(b.Name, name))
	})
	return out[:min(len(out), MaxResults)], nil
}

func (c *Client) fetch(ctx context.Context, url string) ([]result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		Results []result `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Results, nil
}

type result struct {
	Name        string  `json:"name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	CountryCode string  `json:"country_code"`
	Admin1      string  `json:"admin1"`
	Admin2      string  `json:"admin2"`
	Population  int     `json:"population"`
	FeatureCode string  `json:"feature_code"`
}

// rank orders exact name matches (and ZIP searches, whose results are
// named for the town) before fuzzy ones.
func rank(got, query string) int {
	if strings.EqualFold(got, query) || zipRe.MatchString(query) {
		return 0
	}
	return 1
}

// splitQuery splits "Springfield, IL" into ("Springfield", "IL"). A
// trailing ", USA" / ", US" is dropped.
func splitQuery(q string) (name, qualifier string) {
	parts := strings.Split(q, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if n := len(parts); n > 1 {
		switch strings.ToUpper(parts[n-1]) {
		case "US", "USA", "UNITED STATES":
			parts = parts[:n-1]
		}
	}
	name = parts[0]
	if len(parts) > 1 {
		qualifier = parts[len(parts)-1]
	}
	return name, qualifier
}

func matchesState(p Place, q string) bool {
	return strings.EqualFold(q, p.StateCode) || strings.EqualFold(q, p.State)
}

// ParseCoords accepts "lat,lon" (e.g. "39.7392,-104.9847", spaces allowed).
func ParseCoords(s string) (lat, lon float64, ok bool) {
	a, b, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

// stateCode returns the postal abbreviation for a place in NWS coverage.
func stateCode(country, admin1 string) (string, bool) {
	switch country {
	case "US":
		code, ok := usStates[admin1]
		return code, ok || admin1 == ""
	case "PR", "GU", "VI", "AS", "MP":
		return country, true
	}
	return "", false
}

var usStates = map[string]string{
	"Alabama": "AL", "Alaska": "AK", "Arizona": "AZ", "Arkansas": "AR", "California": "CA",
	"Colorado": "CO", "Connecticut": "CT", "Delaware": "DE", "District of Columbia": "DC",
	"Washington, D.C.": "DC", "Florida": "FL", "Georgia": "GA", "Hawaii": "HI", "Idaho": "ID",
	"Illinois": "IL", "Indiana": "IN", "Iowa": "IA", "Kansas": "KS", "Kentucky": "KY",
	"Louisiana": "LA", "Maine": "ME", "Maryland": "MD", "Massachusetts": "MA", "Michigan": "MI",
	"Minnesota": "MN", "Mississippi": "MS", "Missouri": "MO", "Montana": "MT", "Nebraska": "NE",
	"Nevada": "NV", "New Hampshire": "NH", "New Jersey": "NJ", "New Mexico": "NM", "New York": "NY",
	"North Carolina": "NC", "North Dakota": "ND", "Ohio": "OH", "Oklahoma": "OK", "Oregon": "OR",
	"Pennsylvania": "PA", "Rhode Island": "RI", "South Carolina": "SC", "South Dakota": "SD",
	"Tennessee": "TN", "Texas": "TX", "Utah": "UT", "Vermont": "VT", "Virginia": "VA",
	"Washington": "WA", "West Virginia": "WV", "Wisconsin": "WI", "Wyoming": "WY",
}

// Package api is a small client for api.weather.gov.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.weather.gov"

// Version is reported in the User-Agent and by --version. Release builds
// set it with -ldflags "-X github.com/j4mes/ttywx/api.Version=v1.2.3".
var Version = "dev"

// Client fetches and decodes NWS API resources. It is safe for concurrent use.
type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client

	// Retries is how many extra attempts are made after a retryable failure
	// (network error, 429 or 5xx). Backoff doubles from RetryBase.
	Retries   int
	RetryBase time.Duration

	// SI requests forecasts in metric units, which also makes the NWS
	// forecast prose metric ("high near 19, wind 9 km/h"). Temperatures
	// are still converted back to °F so callers see one unit.
	SI bool

	sem chan struct{}
}

// NewClient returns a client that identifies itself with the given contact.
func NewClient(contact string) *Client {
	return &Client{
		BaseURL:   DefaultBaseURL,
		UserAgent: fmt.Sprintf("ttywx/%s (contact: %s)", Version, contact),
		HTTP:      &http.Client{Timeout: 20 * time.Second},
		Retries:   3,
		RetryBase: time.Second,
		// Cap concurrent requests so a refresh of many locations doesn't
		// hammer the API (and trip its rate limiting).
		sem: make(chan struct{}, 4),
	}
}

// HTTPError is returned for non-2xx responses.
type HTTPError struct {
	URL    string
	Status int
	Detail string
}

func (e *HTTPError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("HTTP %d from %s", e.Status, e.URL)
}

func retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	// Anything else that isn't a context cancellation is a transport error
	// (DNS, reset, timeout) and worth another try.
	return !errors.Is(err, context.Canceled)
}

// getJSON fetches url (absolute, or a path relative to BaseURL) into v,
// retrying with exponential backoff on transient failures.
func (c *Client) getJSON(ctx context.Context, url string, v any) error {
	if !strings.HasPrefix(url, "http") {
		url = c.BaseURL + url
	}
	var err error
	for attempt := 0; ; attempt++ {
		err = c.getOnce(ctx, url, v)
		if err == nil || attempt >= c.Retries || !retryable(err) || ctx.Err() != nil {
			return err
		}
		// 1s, 2s, 4s... plus up to 50% jitter.
		d := c.RetryBase << attempt
		d += time.Duration(rand.Int64N(int64(d)/2 + 1))
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return err
		}
	}
}

func (c *Client) getOnce(ctx context.Context, url string, v any) error {
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/geo+json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		he := &HTTPError{URL: url, Status: resp.StatusCode}
		// NWS errors are application/problem+json with a useful "detail".
		var prob struct {
			Detail string `json:"detail"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(body, &prob) == nil {
			he.Detail = prob.Detail
			if he.Detail == "" {
				he.Detail = prob.Title
			}
		}
		return he
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decoding %s: %w", url, err)
	}
	return nil
}

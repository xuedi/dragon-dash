// Package promql is a minimal client for the Prometheus HTTP query API.
//
// dragon-dash reads every metric from Prometheus rather than from the machine
// directly. That costs a dependency but buys history and filtering for free,
// and keeps the dashboard stateless: it stores no samples of its own.
package promql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("prometheus URL is not configured")

type Client struct {
	// base is a func, not a string: the URL is editable in Settings at
	// runtime and we want the next query to pick up the change.
	base func() string
	http *http.Client
}

func New(base func() string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 10 * time.Second}}
}

// Sample is one instant value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Series is one labelled time series.
type Series struct {
	Labels map[string]string
	Points []Point
}

type Point struct {
	T time.Time
	V float64
}

type apiEnvelope struct {
	Status    string          `json:"status"`
	Error     string          `json:"error"`
	ErrorType string          `json:"errorType"`
	Data      json.RawMessage `json:"data"`
}

type resultBody struct {
	ResultType string `json:"resultType"`
	Result     []struct {
		Metric map[string]string `json:"metric"`
		Value  []any             `json:"value"`
		Values [][]any           `json:"values"`
	} `json:"result"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*resultBody, error) {
	base := strings.TrimSuffix(c.base(), "/")
	if base == "" {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying prometheus: %w", err)
	}
	defer resp.Body.Close()

	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		// A non-JSON body almost always means the URL points at something
		// that is not Prometheus, so say that rather than "invalid character".
		return nil, fmt.Errorf("prometheus at %s returned a non-JSON response (HTTP %d)", base, resp.StatusCode)
	}
	if env.Status != "success" {
		if env.Error != "" {
			return nil, fmt.Errorf("prometheus: %s (%s)", env.Error, env.ErrorType)
		}
		return nil, fmt.Errorf("prometheus returned HTTP %d", resp.StatusCode)
	}
	var body resultBody
	if err := json.Unmarshal(env.Data, &body); err != nil {
		return nil, err
	}
	return &body, nil
}

// Query runs an instant query.
func (c *Client) Query(ctx context.Context, expr string) ([]Sample, error) {
	body, err := c.get(ctx, "/api/v1/query", url.Values{"query": {expr}})
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(body.Result))
	for _, r := range body.Result {
		v, ok := scalar(r.Value)
		if !ok {
			continue
		}
		out = append(out, Sample{Labels: r.Metric, Value: v})
	}
	return out, nil
}

// QueryOne runs an instant query expected to return a single value.
// Returns ok=false when the query returned nothing, which is not an error -
// a metric can legitimately be absent (no such device, exporter still starting).
func (c *Client) QueryOne(ctx context.Context, expr string) (float64, bool, error) {
	s, err := c.Query(ctx, expr)
	if err != nil {
		return 0, false, err
	}
	if len(s) == 0 {
		return 0, false, nil
	}
	return s[0].Value, true, nil
}

// QueryRange runs a range query.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) ([]Series, error) {
	q := url.Values{
		"query": {expr},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.Itoa(int(step.Seconds()))},
	}
	body, err := c.get(ctx, "/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	out := make([]Series, 0, len(body.Result))
	for _, r := range body.Result {
		s := Series{Labels: r.Metric, Points: make([]Point, 0, len(r.Values))}
		for _, pair := range r.Values {
			v, ok := scalar(pair)
			if !ok {
				continue
			}
			ts, _ := pair[0].(float64)
			s.Points = append(s.Points, Point{T: time.Unix(int64(ts), 0), V: v})
		}
		out = append(out, s)
	}
	return out, nil
}

// scalar pulls the value out of Prometheus's [timestamp, "value"] pair.
// The value arrives as a *string*, including "NaN", "+Inf" and "-Inf".
func scalar(pair []any) (float64, bool) {
	if len(pair) != 2 {
		return 0, false
	}
	s, ok := pair[1].(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

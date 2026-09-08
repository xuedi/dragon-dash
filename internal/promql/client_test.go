package promql

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScalarParsesPrometheusStringValues(t *testing.T) {
	// Prometheus sends sample values as JSON *strings*, not numbers, and the
	// set includes NaN and the infinities. Anything unparseable must be
	// dropped rather than silently becoming zero.
	tests := []struct {
		name string
		in   []any
		want float64
		ok   bool
	}{
		{"integer", []any{1.0, "42"}, 42, true},
		{"float", []any{1.0, "3.5"}, 3.5, true},
		{"negative", []any{1.0, "-0.25"}, -0.25, true},
		{"exponent", []any{1.0, "1.5e3"}, 1500, true},
		{"positive infinity", []any{1.0, "+Inf"}, math.Inf(1), true},
		{"negative infinity", []any{1.0, "-Inf"}, math.Inf(-1), true},
		{"wrong length", []any{"42"}, 0, false},
		{"number not string", []any{1.0, 42.0}, 0, false},
		{"garbage", []any{1.0, "not-a-number"}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := scalar(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("value = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScalarNaN(t *testing.T) {
	// NaN parses successfully but never equals itself, so it needs its own case.
	got, ok := scalar([]any{1.0, "NaN"})
	if !ok {
		t.Fatal("NaN should parse")
	}
	if !math.IsNaN(got) {
		t.Fatalf("got %v, want NaN", got)
	}
}

func TestQueryOneEmptyResultIsNotAnError(t *testing.T) {
	// An absent metric is normal - no such device, or the exporter has not
	// started. It must not surface as an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	c := New(func() string { return srv.URL })
	_, ok, err := c.QueryOne(context.Background(), "up")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("ok should be false for an empty result")
	}
}

func TestUnconfiguredURL(t *testing.T) {
	c := New(func() string { return "" })
	if _, err := c.Query(context.Background(), "up"); err != ErrNotConfigured {
		t.Fatalf("got %v, want ErrNotConfigured", err)
	}
}

func TestQueryReportsPrometheusErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer srv.Close()

	c := New(func() string { return srv.URL })
	_, err := c.Query(context.Background(), "((")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestQueryRangeDropsUnparseablePoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"x"},"values":[[1,"1"],[2,"bad"],[3,"3"]]}]}}`))
	}))
	defer srv.Close()

	c := New(func() string { return srv.URL })
	series, err := c.QueryRange(context.Background(), "x", time.Unix(1, 0), time.Unix(3, 0), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("got %d series, want 1", len(series))
	}
	if got := len(series[0].Points); got != 2 {
		t.Fatalf("got %d points, want 2 (the unparseable one dropped)", got)
	}
}

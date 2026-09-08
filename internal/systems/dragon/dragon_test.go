package dragon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dragon-dash/internal/promql"
)

// fakeProm answers query_range from a table keyed by a substring of the query,
// so a test can give each of the Thermals chart's queries a different shape.
func fakeProm(t *testing.T, bodies map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		for match, result := range bodies {
			if strings.Contains(q, match) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":` + result + `}}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type rangeResponse struct {
	Unit     string `json:"unit"`
	FromZero bool   `json:"fromZero"`
	Time     []int64
	Series   []struct {
		Name string
		Data []*float64
	}
}

func getRange(t *testing.T, srv *httptest.Server, metric string) rangeResponse {
	t.Helper()
	d := &Dragon{prom: promql.New(func() string { return srv.URL })}
	rec := httptest.NewRecorder()
	d.handleRange(rec, httptest.NewRequest(http.MethodGet, "/range?metric="+metric+"&window=3600", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out rangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding response: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestSeriesNameUsesThermalZoneType(t *testing.T) {
	cases := map[string]string{
		"cpuss0-thermal": "CPU",
		"ddr-thermal":    "DDR",
		"quiet-thermal":  "quiet-thermal", // not one of the named six
	}
	for zone, want := range cases {
		if got := seriesName(map[string]string{"type": zone, "instance": "x:9100"}, "Thermals"); got != want {
			t.Errorf("seriesName(type=%q) = %q, want %q", zone, got, want)
		}
	}
	if got := seriesName(map[string]string{"chip": "nvme_nvme0", "sensor": "temp1"}, "Thermals"); got != "nvme_nvme0" {
		t.Errorf("hwmon series named %q", got)
	}
}

// A series with no data for part of the window is missing those points
// entirely, so the lines have to be aligned by timestamp. Aligning by index
// would silently plot the second series an hour to the left.
func TestRangeAlignsSeriesByTimestamp(t *testing.T) {
	srv := fakeProm(t, map[string]string{
		"max(node_thermal_zone_temp)": `[{"metric":{},"values":[[100,"40"],[160,"41"],[220,"42"]]}]`,
		"type=~":                      `[{"metric":{"type":"ddr-thermal"},"values":[[160,"38"],[220,"39"]]}]`,
	})
	out := getRange(t, srv, "thermals")

	if want := []int64{100, 160, 220}; len(out.Time) != 3 || out.Time[0] != want[0] || out.Time[2] != want[2] {
		t.Fatalf("time = %v, want %v", out.Time, want)
	}
	if len(out.Series) != 2 {
		t.Fatalf("got %d series, want 2 (absent metrics contribute no line)", len(out.Series))
	}
	ddr := out.Series[1]
	if ddr.Name != "DDR" {
		t.Errorf("series name = %q, want DDR", ddr.Name)
	}
	if ddr.Data[0] != nil {
		t.Errorf("missing sample rendered as %v, want null", *ddr.Data[0])
	}
	if ddr.Data[1] == nil || *ddr.Data[1] != 38 {
		t.Errorf("sample at 160 = %v, want 38", ddr.Data[1])
	}
}

// Prometheus sends NaN and the infinities as values like any other. They are
// not encodable as JSON numbers, and encoding/json fails the whole response
// rather than the one sample, so they have to be dropped on the way out.
func TestRangeDropsNonFiniteSamples(t *testing.T) {
	srv := fakeProm(t, map[string]string{
		"node_memory_MemAvailable_bytes": `[{"metric":{},"values":[[100,"NaN"],[160,"50"],[220,"+Inf"]]}]`,
	})
	out := getRange(t, srv, "memory")

	if len(out.Series) != 1 {
		t.Fatalf("got %d series, want 1", len(out.Series))
	}
	data := out.Series[0].Data
	if len(data) != 3 {
		t.Fatalf("data has %d slots, want 3", len(data))
	}
	if data[0] != nil || data[2] != nil {
		t.Errorf("non-finite samples survived: %v", data)
	}
	if data[1] == nil || *data[1] != 50 {
		t.Errorf("finite sample = %v, want 50", data[1])
	}
	if !out.FromZero {
		t.Error("percentage chart should pin its axis at zero")
	}
}

func TestThermalsChartIsNotZeroBased(t *testing.T) {
	for _, c := range charts {
		if c.Slug == "thermals" && c.FromZero {
			t.Error("a zero based axis flattens every thermal line into one")
		}
	}
}

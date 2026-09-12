package host

import (
	"strings"
	"testing"
)

func TestSeriesNameUsesThermalZoneType(t *testing.T) {
	cases := map[string]string{
		"cpuss0-thermal": "CPU",
		"ddr-thermal":    "DDR",
		"quiet-thermal":  "quiet-thermal", // not one of the named six
	}
	for zone, want := range cases {
		if got := seriesName(map[string]string{"type": zone, "instance": "x:9100"}); got != want {
			t.Errorf("seriesName(type=%q) = %q, want %q", zone, got, want)
		}
	}
	if got := seriesName(map[string]string{"chip": "nvme_nvme0", "sensor": "temp1"}); got != "nvme_nvme0" {
		t.Errorf("hwmon series named %q", got)
	}
	if got := seriesName(map[string]string{}); got != "" {
		t.Errorf("label-less series named %q, want the chart title to apply", got)
	}
}

// The kernel renumbers thermal zones across boots, so an unaggregated query
// returns one series per numbering era and the chart draws the same sensor
// several times under the same name. Every thermals query has to collapse the
// labels it does not name lines by.
func TestThermalsQueriesAreAggregated(t *testing.T) {
	for _, c := range charts {
		if c.Slug != "thermals" {
			continue
		}
		for _, cs := range c.Series {
			if !strings.HasPrefix(cs.Query, "max") {
				t.Errorf("query %q is not aggregated: a reboot would fork it into several lines", cs.Query)
			}
		}
	}
}

func TestThermalsChartIsNotZeroBased(t *testing.T) {
	for _, c := range charts {
		if c.Slug == "thermals" && c.FromZero {
			t.Error("a zero based axis flattens every thermal line into one")
		}
	}
}

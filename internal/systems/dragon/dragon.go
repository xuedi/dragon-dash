// Package dragon is the server-metrics system: CPU, memory, storage, thermals.
//
// Every value comes from Prometheus scraping prometheus-node-exporter, not from
// reading /proc locally. That is deliberate, it means the same code works when
// dragon-dash runs on a different machine from the one being measured, and every
// number on screen has history behind it.
package dragon

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"dragon-dash/internal/promql"
	"dragon-dash/internal/system"
)

//go:embed templates/*.html
var templatesFS embed.FS

func init() { system.Register(&Dragon{}) }

// chart describes one graphable metric. Adding a graph is adding an entry here.
//
// A chart is either a single Query or a list of named Series. Thermals is the
// multi query case: one hottest-of-everything line cannot say which component
// is warm, and the two SSDs are not even in the same metric.
type chart struct {
	Slug   string
	Title  string
	Query  string
	Unit   string
	Series []chartSeries

	// FromZero pins the y axis at zero. True for percentages, where the
	// distance from zero is the whole point. False for temperatures, where
	// the interesting spread is a few degrees and a zero based axis would
	// flatten every line on top of every other.
	FromZero bool
}

// chartSeries is one line, or several: an empty Name means the query returns
// more than one series and each is named from its own labels.
type chartSeries struct {
	Name  string
	Query string
}

var charts = []chart{
	{Slug: "cpu", Title: "CPU", Unit: "%", FromZero: true,
		Query: `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`},
	{Slug: "memory", Title: "Memory", Unit: "%", FromZero: true,
		Query: `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`},
	{Slug: "storage", Title: "Storage", Unit: "%", FromZero: true,
		Query: `100 * (1 - node_filesystem_avail_bytes{fstype!~"tmpfs|ramfs"} / node_filesystem_size_bytes{fstype!~"tmpfs|ramfs"})`},
	{Slug: "thermals", Title: "Thermals", Unit: "°C", Series: []chartSeries{
		{Name: "Hottest zone", Query: `max(node_thermal_zone_temp)`},
		// One query, six lines, named from the type label. Listing the zones
		// separately would cost six round trips for the same data.
		{Query: `node_thermal_zone_temp{type=~"` + strings.Join(namedZones, "|") + `"}`},
		// temp1 is the drive's own Composite reading. temp2 and temp3 are the
		// two individual sensors it is derived from.
		{Name: "Internal SSD", Query: `node_hwmon_temp_celsius{chip="nvme_nvme0",sensor="temp1"}`},
		{Name: "External SSD", Query: externalSSDTemp},
	}},
}

// externalSSDTemp reads the USB drive, which reaches Prometheus through a
// node_exporter textfile collector on the server rather than through a
// collector proper: it is a SCSI disk behind a UAS bridge with no hwmon device
// and no thermal zone, so its temperature only exists over SMART, which needs
// root. The name matches smartctl_exporter so swapping the script for the real
// exporter would not touch this query.
//
// max() guards against the series multiplying. The collector publishes exactly
// one drive; a real smartctl_exporter covering both would need a device filter
// here instead.
const externalSSDTemp = `max(smartctl_device_temperature{temperature_type="current"})`

// namedZones are the thermal zones worth a line of their own, the same six the
// server's own ~/bin/temps summarises. The board has 34 in total, most of them
// within a degree of each other.
var namedZones = []string{
	"cpuss0-thermal",
	"cpuss1-thermal",
	"gpuss0-thermal",
	"ddr-thermal",
	"pm8350c-thermal",
	"msm-skin-thermal",
}

// zoneNames give those zones the short labels ~/bin/temps prints. A zone that
// is not listed keeps its kernel name.
var zoneNames = map[string]string{
	"cpuss0-thermal":   "CPU",
	"cpuss1-thermal":   "CPU2",
	"gpuss0-thermal":   "GPU",
	"ddr-thermal":      "DDR",
	"pm8350c-thermal":  "PMIC",
	"msm-skin-thermal": "Skin",
}

// series is the chart's lines, whether it declared one query or several.
func (c chart) series() []chartSeries {
	if len(c.Series) > 0 {
		return c.Series
	}
	return []chartSeries{{Query: c.Query}}
}

type Dragon struct {
	tmpl *template.Template
	deps system.Deps
	prom *promql.Client
}

func (d *Dragon) ID() string    { return "dragon" }
func (d *Dragon) Title() string { return "Dragon" }

func (d *Dragon) Nav() []system.NavItem {
	nav := []system.NavItem{{Slug: "overview", Title: "Overview"}}
	for _, c := range charts {
		nav = append(nav, system.NavItem{Slug: c.Slug, Title: c.Title})
	}
	return nav
}

// ConfigSchema is empty: this system needs nothing beyond the core Prometheus
// URL, which the shell owns.
func (d *Dragon) ConfigSchema() []system.ConfigField { return nil }

func (d *Dragon) Register(mux *http.ServeMux, prefix string, deps system.Deps) {
	d.deps = deps
	d.prom = promql.New(deps.PromURL)
	d.tmpl = system.MustTemplates(templatesFS, "templates/*.html")
	mux.HandleFunc("GET "+prefix+"range", d.handleRange)
}

type statCard struct {
	Label   string
	Display string
	Sub     string
	OK      bool
}

type overviewData struct {
	Top          system.PageTop
	Stats        []statCard
	Err          string
	Unconfigured bool
}

func (d *Dragon) Render(slug string, r *http.Request) (template.HTML, error) {
	if slug == "overview" {
		return d.renderOverview(r)
	}
	for _, c := range charts {
		if c.Slug == slug {
			return d.renderChart(c)
		}
	}
	return "", fmt.Errorf("unknown page %q", slug)
}

func (d *Dragon) renderChart(c chart) (template.HTML, error) {
	top := system.PageTop{Title: c.Title}
	if len(c.Series) > 0 {
		// Four queries will not fit in the infobar, so the bar carries the
		// count and the queries themselves hang off the tooltip.
		qs := make([]string, 0, len(c.Series))
		for _, cs := range c.Series {
			qs = append(qs, cs.Query)
		}
		top.Infof(`<span class="has-text-grey is-size-7" title="%s">%d queries</span>`,
			template.HTMLEscapeString(strings.Join(qs, "\n")), len(c.Series))
	} else {
		top.Infof(`<code class="is-size-7">%s</code>`, template.HTMLEscapeString(c.Query))
	}
	top.Actionf(`<span id="dd-status" class="tag is-light">loading</span>`)
	top.Actionf(`<div class="select is-small">
      <select id="dd-range" onchange="ddLoad()">
        <option value="3600" selected>last hour</option>
        <option value="21600">last 6 hours</option>
        <option value="86400">last day</option>
        <option value="604800">last week</option>
        <option value="2592000">last 30 days</option>
        <option value="31536000">last year</option>
      </select></div>`)

	return d.exec("chart", struct {
		Top          system.PageTop
		Title        string
		Metric       string
		Unconfigured bool
	}{
		Top:          top,
		Title:        c.Title,
		Metric:       c.Slug,
		Unconfigured: d.deps.PromURL() == "",
	})
}

func (d *Dragon) renderOverview(r *http.Request) (template.HTML, error) {
	data := overviewData{Unconfigured: d.deps.PromURL() == ""}
	data.Top = system.PageTop{Title: "Overview"}
	if url := d.deps.PromURL(); url != "" {
		data.Top.Infof(`<span class="has-text-grey is-size-7">%s</span>`, template.HTMLEscapeString(url))
	}
	if data.Unconfigured {
		return d.exec("overview", data)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	type probe struct {
		label  string
		expr   string
		format func(float64) (string, string)
	}
	probes := []probe{
		{"CPU busy", `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`,
			func(v float64) (string, string) { return fmt.Sprintf("%.1f %%", v), "" }},
		{"Memory used", `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`,
			func(v float64) (string, string) { return fmt.Sprintf("%.1f %%", v), "" }},
		{"Root filesystem", `100 * (1 - node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})`,
			func(v float64) (string, string) { return fmt.Sprintf("%.1f %%", v), "" }},
		{"Load (1m)", `node_load1`,
			func(v float64) (string, string) { return fmt.Sprintf("%.2f", v), "" }},
		// node_thermal_zone_temp, not node_hwmon_temp_celsius: the latter
		// carries the internal NVMe alongside the board zones, so the card
		// could report the drive while claiming to report the board.
		{"Hottest zone", `max(node_thermal_zone_temp)`,
			func(v float64) (string, string) { return fmt.Sprintf("%.1f °C", v), "" }},
		{"External SSD", externalSSDTemp,
			func(v float64) (string, string) { return fmt.Sprintf("%.1f °C", v), "" }},
		{"Uptime", `time() - node_boot_time_seconds`,
			func(v float64) (string, string) { return humanDuration(time.Duration(v) * time.Second), "" }},
	}

	for _, p := range probes {
		v, ok, err := d.prom.QueryOne(ctx, p.expr)
		if err != nil {
			// One error is enough; they will all be the same connection problem.
			data.Err = err.Error()
			data.Stats = append(data.Stats, statCard{Label: p.label})
			continue
		}
		card := statCard{Label: p.label, OK: ok}
		if ok {
			card.Display, card.Sub = p.format(v)
		}
		data.Stats = append(data.Stats, card)
	}
	return d.exec("overview", data)
}

func (d *Dragon) exec(name string, data any) (template.HTML, error) {
	var buf []byte
	w := &byteWriter{buf: &buf}
	if err := d.tmpl.ExecuteTemplate(w, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf), nil
}

type byteWriter struct{ buf *[]byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// handleRange feeds the uPlot charts.
func (d *Dragon) handleRange(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	var c *chart
	for i := range charts {
		if charts[i].Slug == metric {
			c = &charts[i]
			break
		}
	}
	if c == nil {
		http.Error(w, "unknown metric", http.StatusNotFound)
		return
	}

	window, err := strconv.Atoi(r.URL.Query().Get("window"))
	if err != nil || window <= 0 {
		window = 86400
	}
	end := time.Now()
	start := end.Add(-time.Duration(window) * time.Second)

	// Aim for ~800 points regardless of range, so a year costs no more to
	// draw than an hour.
	step := time.Duration(window/800) * time.Second
	if step < 15*time.Second {
		step = 15 * time.Second
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	type line struct {
		name   string
		points []promql.Point
	}
	var lines []line
	for _, cs := range c.series() {
		res, err := d.prom.QueryRange(ctx, cs.Query, start, end, step)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// No result is not an error: the external drive may be unplugged, or
		// its collector not installed yet. The line is simply absent.
		for _, sr := range res {
			name := cs.Name
			if name == "" {
				name = seriesName(sr.Labels, c.Title)
			}
			lines = append(lines, line{name: name, points: sr.Points})
		}
	}

	// Every query shares start, end and step, so Prometheus hands back the
	// same timestamp grid. A series with no data for part of the window is
	// missing those points entirely though, so the lines are aligned by
	// timestamp rather than by index.
	at := map[int64]int{}
	times := []int64{}
	for _, l := range lines {
		for _, p := range l.points {
			t := p.T.Unix()
			if _, ok := at[t]; !ok {
				at[t] = 0
				times = append(times, t)
			}
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for i, t := range times {
		at[t] = i
	}

	out := struct {
		Unit     string       `json:"unit"`
		FromZero bool         `json:"fromZero"`
		Time     []int64      `json:"time"`
		Series   []jsonSeries `json:"series"`
	}{Unit: c.Unit, FromZero: c.FromZero, Time: times, Series: []jsonSeries{}}

	for _, l := range lines {
		data := make([]*float64, len(times))
		for _, p := range l.points {
			// NaN and the infinities are legal Prometheus values and cannot be
			// encoded as JSON numbers. A nil slot draws a gap, which is what
			// they mean anyway.
			if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
				continue
			}
			v := p.V
			data[at[p.T.Unix()]] = &v
		}
		out.Series = append(out.Series, jsonSeries{Name: l.name, Data: data})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// jsonSeries is one line as the chart consumes it. Data is pointers so a
// missing sample can be null rather than a plausible looking zero.
type jsonSeries struct {
	Name string     `json:"name"`
	Data []*float64 `json:"data"`
}

// seriesName picks something human out of the label set, falling back to the
// chart title when the query aggregated all labels away.
func seriesName(labels map[string]string, fallback string) string {
	if t := labels["type"]; t != "" {
		if n, ok := zoneNames[t]; ok {
			return n
		}
		return t
	}
	for _, k := range []string{"mountpoint", "device", "chip", "sensor", "instance"} {
		if v, ok := labels[k]; ok && v != "" {
			return v
		}
	}
	return fallback
}

func humanDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}

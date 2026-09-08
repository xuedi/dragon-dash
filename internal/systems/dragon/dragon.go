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
	"net/http"
	"strconv"
	"time"

	"dragon-dash/internal/promql"
	"dragon-dash/internal/system"
)

//go:embed templates/*.html
var templatesFS embed.FS

func init() { system.Register(&Dragon{}) }

// chart describes one graphable metric. Adding a graph is adding an entry here.
type chart struct {
	Slug  string
	Title string
	Query string
	Unit  string
}

var charts = []chart{
	{"cpu", "CPU", `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`, "%"},
	{"memory", "Memory", `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`, "%"},
	{"storage", "Storage", `100 * (1 - node_filesystem_avail_bytes{fstype!~"tmpfs|ramfs"} / node_filesystem_size_bytes{fstype!~"tmpfs|ramfs"})`, "%"},
	{"thermals", "Thermals", `max(node_hwmon_temp_celsius)`, "°C"},
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
	return d.exec("chart", struct {
		Title, Metric, Query string
		Unconfigured         bool
	}{
		Title:        c.Title,
		Metric:       c.Slug,
		Query:        c.Query,
		Unconfigured: d.deps.PromURL() == "",
	})
}

func (d *Dragon) renderOverview(r *http.Request) (template.HTML, error) {
	data := overviewData{Unconfigured: d.deps.PromURL() == ""}
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
		{"Hottest sensor", `max(node_hwmon_temp_celsius)`,
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

	series, err := d.prom.QueryRange(ctx, c.Query, start, end, step)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	out := struct {
		Unit   string  `json:"unit"`
		Time   []int64 `json:"time"`
		Series []struct {
			Name string    `json:"name"`
			Data []float64 `json:"data"`
		} `json:"series"`
	}{Unit: c.Unit, Time: []int64{}}

	if len(series) > 0 {
		for _, p := range series[0].Points {
			out.Time = append(out.Time, p.T.Unix())
		}
		for _, s := range series {
			vals := make([]float64, 0, len(s.Points))
			for _, p := range s.Points {
				vals = append(vals, p.V)
			}
			out.Series = append(out.Series, struct {
				Name string    `json:"name"`
				Data []float64 `json:"data"`
			}{Name: seriesName(s.Labels, c.Title), Data: vals})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// seriesName picks something human out of the label set, falling back to the
// chart title when the query aggregated all labels away.
func seriesName(labels map[string]string, fallback string) string {
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

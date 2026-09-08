// Package fritzhome shows FRITZ!Box smart home data: plug power, energy and
// room temperatures, plus a floor plan of where the devices actually are.
//
// Note what is NOT here: FRITZ!Box credentials. dragon-dash never talks to the
// router. fritz_exporter does, and it holds its own copy of the password. Two
// places storing the same secret is worse than one, so this system reads
// everything from Prometheus like every other system.
package fritzhome

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"dragon-dash/internal/promql"
	"dragon-dash/internal/system"
)

//go:embed templates/*.html
var templatesFS embed.FS

func init() { system.Register(&FritzHome{}) }

const defaultPrefix = "fritz_"

const examplePlan = `{
  "width": 800, "height": 500,
  "rooms": [
    {"name": "Living room", "points": "20,20 400,20 400,300 20,300"},
    {"name": "Kitchen",     "points": "400,20 780,20 780,180 400,180"}
  ],
  "devices": [
    {"label": "Desk plug", "x": 120, "y": 160, "metric": "fritz_homeauto_power_watt", "match": {"name": "Desk"}}
  ]
}`

type FritzHome struct {
	tmpl *template.Template
	deps system.Deps
	prom *promql.Client
}

func (f *FritzHome) ID() string    { return "fritzhome" }
func (f *FritzHome) Title() string { return "FritzHome" }

func (f *FritzHome) Nav() []system.NavItem {
	return []system.NavItem{
		{Slug: "overview", Title: "Overview"},
		{Slug: "floorplan", Title: "Floor plan"},
	}
}

func (f *FritzHome) ConfigSchema() []system.ConfigField {
	return []system.ConfigField{
		{
			Key:     "metric_prefix",
			Label:   "Metric prefix",
			Help:    "Series matching this prefix are treated as smart home data. Confirm against a running exporter before changing.",
			Kind:    system.KindText,
			Default: defaultPrefix,
		},
		{
			Key:   "floorplan",
			Label: "Floor plan (JSON)",
			Help:  "Rooms as polygons, devices as points. Leave empty until the layout is drawn.",
			Kind:  system.KindText,
		},
	}
}

func (f *FritzHome) Register(mux *http.ServeMux, prefix string, deps system.Deps) {
	f.deps = deps
	f.prom = promql.New(deps.PromURL)
	f.tmpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))
}

func (f *FritzHome) prefix() string {
	if p := f.deps.Config.Get("metric_prefix"); p != "" {
		return p
	}
	return defaultPrefix
}

func (f *FritzHome) Render(slug string, r *http.Request) (template.HTML, error) {
	switch slug {
	case "overview":
		return f.renderOverview(r)
	case "floorplan":
		return f.renderFloorplan(r)
	}
	return "", fmt.Errorf("unknown page %q", slug)
}

type metricRow struct{ Name, Value string }

type deviceRow struct {
	Name    string
	Metrics []metricRow
}

func (f *FritzHome) renderOverview(r *http.Request) (template.HTML, error) {
	data := struct {
		Devices      []deviceRow
		Err          string
		Prefix       string
		Unconfigured bool
	}{Prefix: f.prefix(), Unconfigured: f.deps.PromURL() == ""}

	if data.Unconfigured {
		return f.exec("overview", data)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Discover rather than hard-code: the exporter's exact metric names depend
	// on its version and on which devices are paired, so ask Prometheus what
	// actually exists instead of guessing.
	samples, err := f.prom.Query(ctx, fmt.Sprintf(`{__name__=~"%s.*"}`, regexpEscape(f.prefix())))
	if err != nil {
		data.Err = err.Error()
		return f.exec("overview", data)
	}

	byDevice := map[string][]metricRow{}
	for _, s := range samples {
		name := deviceLabel(s.Labels)
		byDevice[name] = append(byDevice[name], metricRow{
			Name:  s.Labels["__name__"],
			Value: formatValue(s.Value),
		})
	}
	names := make([]string, 0, len(byDevice))
	for n := range byDevice {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rows := byDevice[n]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		data.Devices = append(data.Devices, deviceRow{Name: n, Metrics: rows})
	}
	return f.exec("overview", data)
}

// plan is the on-disk floor plan format. Kept deliberately simple so it can be
// hand-written or generated from a drawing.
type plan struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	Rooms  []struct {
		Name   string `json:"name"`
		Points string `json:"points"`
	} `json:"rooms"`
	Devices []struct {
		Label  string            `json:"label"`
		X      float64           `json:"x"`
		Y      float64           `json:"y"`
		Metric string            `json:"metric"`
		Match  map[string]string `json:"match"`
	} `json:"devices"`
}

func (f *FritzHome) renderFloorplan(r *http.Request) (template.HTML, error) {
	type room struct {
		Name, Points   string
		LabelX, LabelY float64
	}
	type dev struct {
		Name, Value, Colour string
		X, Y, LabelY        float64
	}
	data := struct {
		HasPlan bool
		Example string
		W, H    int
		Rooms   []room
		Devices []dev
	}{Example: examplePlan, W: 800, H: 500}

	raw := f.deps.Config.Get("floorplan")
	if strings.TrimSpace(raw) == "" {
		return f.exec("floorplan", data)
	}

	var p plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", fmt.Errorf("floor plan JSON is invalid: %w", err)
	}
	data.HasPlan = true
	if p.Width > 0 {
		data.W = p.Width
	}
	if p.Height > 0 {
		data.H = p.Height
	}
	for _, rm := range p.Rooms {
		x, y := firstPoint(rm.Points)
		data.Rooms = append(data.Rooms, room{Name: rm.Name, Points: rm.Points, LabelX: x + 8, LabelY: y + 20})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, d := range p.Devices {
		value := "-"
		if d.Metric != "" && f.deps.PromURL() != "" {
			if v, ok, err := f.prom.QueryOne(ctx, selector(d.Metric, d.Match)); err == nil && ok {
				value = formatValue(v)
			}
		}
		data.Devices = append(data.Devices, dev{
			Name: d.Label, Value: value, Colour: "#485fc7",
			X: d.X, Y: d.Y, LabelY: d.Y + 26,
		})
	}
	return f.exec("floorplan", data)
}

func (f *FritzHome) exec(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := f.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// deviceLabel picks whichever label the exporter uses to name a device.
func deviceLabel(labels map[string]string) string {
	for _, k := range []string{"name", "device_name", "ain", "device", "id"} {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return "(unlabelled)"
}

func selector(metric string, match map[string]string) string {
	if len(match) == 0 {
		return metric
	}
	parts := make([]string, 0, len(match))
	for k, v := range match {
		parts = append(parts, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(parts)
	return metric + "{" + strings.Join(parts, ",") + "}"
}

func formatValue(v float64) string {
	switch {
	case v == float64(int64(v)):
		return fmt.Sprintf("%d", int64(v))
	case v < 10:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%.1f", v)
	}
}

func firstPoint(points string) (float64, float64) {
	fields := strings.Fields(points)
	if len(fields) == 0 {
		return 0, 0
	}
	var x, y float64
	_, _ = fmt.Sscanf(fields[0], "%f,%f", &x, &y)
	return x, y
}

// regexpEscape quotes the few characters that could turn a prefix into a
// wildcard. A prefix comes from config, so it is trusted but not necessarily
// regexp-safe.
func regexpEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

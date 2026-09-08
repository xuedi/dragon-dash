// Package fritzhome shows FRITZ!Box smart home data and exposes it as
// Prometheus metrics.
//
// It talks to the box directly over AVM's documented interfaces, so there is
// no separate exporter to run and the credentials live in exactly one place.
// Live values on the page come straight from the box; history comes from
// Prometheus scraping this application's /metrics.
package fritzhome

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dragon-dash/internal/fritzbox"
	"dragon-dash/internal/system"
)

//go:embed templates/*.html
var templatesFS embed.FS

func init() { system.Register(&FritzHome{}) }

const defaultInterval = 60 * time.Second

const examplePlan = `{
  "width": 800, "height": 500,
  "rooms": [
    {"name": "Living room", "points": "20,20 400,20 400,300 20,300"},
    {"name": "Kitchen",     "points": "400,20 780,20 780,180 400,180"}
  ],
  "devices": [
    {"ain": "116300343807", "x": 120, "y": 160},
    {"ain": "139790212573", "x": 300, "y": 240}
  ]
}`

type FritzHome struct {
	tmpl *template.Template
	deps system.Deps
	cli  *fritzbox.Client

	mu        sync.Mutex
	cached    []fritzbox.Device
	cachedAt  time.Time
	cachedErr error
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
		{Key: "url", Label: "FRITZ!Box URL", Kind: system.KindURL, Default: "http://fritz.box",
			Help: "The box itself. TR-064 does not need to be enabled; this uses the AHA HTTP interface."},
		{Key: "username", Label: "Username", Kind: system.KindText,
			Help: "A FRITZ!Box user with the Smart Home permission."},
		{Key: "password", Label: "Password", Kind: system.KindPassword, Secret: true},
		{Key: "interval", Label: "Poll interval", Kind: system.KindText, Default: "60s",
			Help: "How long a reading is reused before the box is asked again."},
		{Key: "floorplan_file", Label: "Floor plan file", Kind: system.KindText,
			Help: "Path to a JSON floor plan. Preferred over the inline value: a traced plan is too long for an environment variable."},
		{Key: "floorplan", Label: "Floor plan (JSON)", Kind: system.KindText,
			Help: "Inline alternative to the file above."},
	}
}

func (f *FritzHome) Register(mux *http.ServeMux, prefix string, deps system.Deps) {
	f.deps = deps
	f.tmpl = system.MustTemplates(templatesFS, "templates/*.html")
	f.cli = fritzbox.New(
		deps.Config.GetOr("url", "http://fritz.box"),
		deps.Config.Get("username"),
		deps.Config.Get("password"),
	)
}

func (f *FritzHome) configured() bool { return f.deps.Config.Get("password") != "" }

func (f *FritzHome) interval() time.Duration {
	d, err := time.ParseDuration(f.deps.Config.GetOr("interval", "60s"))
	if err != nil || d <= 0 {
		return defaultInterval
	}
	return d
}

// devices returns a cached reading, refreshing when it is older than the poll
// interval. Both the page and the /metrics scrape go through here, so opening
// the dashboard during a scrape does not double the load on the box.
func (f *FritzHome) devices(ctx context.Context) ([]fritzbox.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.cachedAt) < f.interval() && (f.cached != nil || f.cachedErr != nil) {
		return f.cached, f.cachedErr
	}
	d, err := f.cli.Devices(ctx)
	f.cached, f.cachedErr, f.cachedAt = d, err, time.Now()
	return d, err
}

// Collect implements system.Collector.
func (f *FritzHome) Collect(ctx context.Context) ([]system.Metric, error) {
	if !f.configured() {
		return nil, nil
	}
	devices, err := f.devices(ctx)
	if err != nil {
		return nil, err
	}

	var out []system.Metric
	add := func(name, help, typ string, d fritzbox.Device, v float64) {
		out = append(out, system.Metric{
			Name: name, Help: help, Type: typ, Value: v,
			Labels: map[string]string{"ain": d.AIN, "name": d.Name, "product": d.Product},
		})
	}
	for _, d := range devices {
		add("fritz_device_present", "1 when the device is reachable", "gauge", d, boolVal(d.Present))
		if d.PowerW != nil {
			add("fritz_power_watts", "Current power draw in watts", "gauge", d, *d.PowerW)
		}
		if d.EnergyKWh != nil {
			add("fritz_energy_kwh_total", "Lifetime energy in kilowatt hours", "counter", d, *d.EnergyKWh)
		}
		if d.VoltageV != nil {
			add("fritz_voltage_volts", "Mains voltage", "gauge", d, *d.VoltageV)
		}
		if d.TempC != nil {
			add("fritz_temperature_celsius", "Measured temperature", "gauge", d, *d.TempC)
		}
		if d.HumidityP != nil {
			add("fritz_humidity_percent", "Relative humidity", "gauge", d, *d.HumidityP)
		}
		if d.SwitchOn != nil {
			add("fritz_switch_on", "1 when the switch is on", "gauge", d, boolVal(*d.SwitchOn))
		}
		if d.TargetC != nil {
			add("fritz_target_temperature_celsius", "Thermostat setpoint", "gauge", d, *d.TargetC)
		}
		if d.BatteryPct != nil {
			add("fritz_battery_percent", "Battery charge", "gauge", d, *d.BatteryPct)
		}
		if d.BatteryLow != nil {
			add("fritz_battery_low", "1 when the battery is low", "gauge", d, boolVal(*d.BatteryLow))
		}
	}
	return out, nil
}

func boolVal(b bool) float64 {
	if b {
		return 1
	}
	return 0
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

type deviceRow struct {
	Name, Product, AIN string
	Present            bool
	Power, Energy      string
	Temp, Humidity     string
	Switch             string
	Target, Battery    string
}

func (f *FritzHome) renderOverview(r *http.Request) (template.HTML, error) {
	data := struct {
		Top          system.PageTop
		Devices      []deviceRow
		Err          string
		Unconfigured bool
	}{Unconfigured: !f.configured()}
	data.Top = system.PageTop{Title: "Devices"}

	if data.Unconfigured {
		return f.exec("overview", data)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	devices, err := f.devices(ctx)
	if err != nil {
		data.Err = err.Error()
		return f.exec("overview", data)
	}

	var total float64
	for _, d := range devices {
		row := deviceRow{Name: d.Name, Product: d.Product, AIN: d.AIN, Present: d.Present}
		if d.PowerW != nil {
			total += *d.PowerW
			row.Power = fmt.Sprintf("%.1f W", *d.PowerW)
		}
		if d.EnergyKWh != nil {
			row.Energy = fmt.Sprintf("%.1f kWh", *d.EnergyKWh)
		}
		if d.TempC != nil {
			row.Temp = fmt.Sprintf("%.1f °C", *d.TempC)
		}
		if d.HumidityP != nil {
			row.Humidity = fmt.Sprintf("%.0f %%", *d.HumidityP)
		}
		if d.SwitchOn != nil {
			row.Switch = "off"
			if *d.SwitchOn {
				row.Switch = "on"
			}
		}
		if d.TargetC != nil {
			row.Target = fmt.Sprintf("%.1f °C", *d.TargetC)
		}
		if d.BatteryPct != nil {
			row.Battery = fmt.Sprintf("%.0f %%", *d.BatteryPct)
		}
		data.Devices = append(data.Devices, row)
	}
	f.mu.Lock()
	age := time.Since(f.cachedAt).Round(time.Second)
	f.mu.Unlock()

	data.Top.Infof(`<span class="has-text-grey is-size-7">%d devices</span>`, len(devices))
	data.Top.Infof(`<span class="has-text-grey is-size-7">read %s ago</span>`, age)
	data.Top.Actionf(`<span class="tag is-primary is-medium">%.1f W total</span>`, total)
	return f.exec("overview", data)
}

// plan is the on-disk floor plan. Deliberately plain geometry so it can be
// traced from a sketch by hand: SVG polygon/polyline point strings, and
// devices placed by AIN so the drawing never repeats a device name.
type plan struct {
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Outline string `json:"outline"` // the flat's outer wall
	Rooms   []struct {
		Name   string  `json:"name"`
		Points string  `json:"points"`
		LabelX float64 `json:"labelX"`
		LabelY float64 `json:"labelY"`
	} `json:"rooms"`
	Walls []string `json:"walls"` // interior walls, as polyline point strings
	Doors []struct {
		X1 float64 `json:"x1"`
		Y1 float64 `json:"y1"`
		X2 float64 `json:"x2"`
		Y2 float64 `json:"y2"`
	} `json:"doors"`
	Devices []struct {
		AIN   string  `json:"ain"`
		Label string  `json:"label"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	} `json:"devices"`
}

// planJSON returns the configured plan, preferring a file over an inline
// value. A traced floor plan runs to a few hundred lines, which is miserable
// inside an environment variable.
func (f *FritzHome) planJSON() (string, error) {
	if path := f.deps.Config.Get("floorplan_file"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading floor plan %s: %w", path, err)
		}
		return string(b), nil
	}
	return f.deps.Config.Get("floorplan"), nil
}

func (f *FritzHome) renderFloorplan(r *http.Request) (template.HTML, error) {
	type room struct {
		Name, Points   string
		LabelX, LabelY float64
	}
	type door struct{ X1, Y1, X2, Y2 float64 }
	type dev struct {
		Name, Value, Unit, Colour   string
		X, Y, LabelY, ValueY, UnitY float64
		Known                       bool
	}
	data := struct {
		Top     system.PageTop
		HasPlan bool
		Example string
		W, H    int
		Outline string
		Rooms   []room
		Walls   []string
		Doors   []door
		Devices []dev
	}{Example: examplePlan, W: 800, H: 500}
	data.Top = system.PageTop{Title: "Floor plan"}
	data.Top.Infof(`<span class="tag is-primary is-light">power</span>`)
	data.Top.Infof(`<span class="tag is-link is-light">temperature</span>`)
	data.Top.Infof(`<span class="tag is-danger is-light">doors</span>`)

	raw, err := f.planJSON()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return f.exec("floorplan", data)
	}
	var p plan
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", fmt.Errorf("floor plan JSON is invalid: %w", err)
	}
	data.HasPlan = true
	data.Outline = p.Outline
	data.Walls = p.Walls
	if p.Width > 0 {
		data.W = p.Width
	}
	if p.Height > 0 {
		data.H = p.Height
	}
	for _, rm := range p.Rooms {
		lx, ly := rm.LabelX, rm.LabelY
		if lx == 0 && ly == 0 {
			x, y := firstPoint(rm.Points)
			lx, ly = x+12, y+26
		}
		data.Rooms = append(data.Rooms, room{Name: rm.Name, Points: rm.Points, LabelX: lx, LabelY: ly})
	}
	for _, d := range p.Doors {
		data.Doors = append(data.Doors, door{d.X1, d.Y1, d.X2, d.Y2})
	}

	byAIN := map[string]fritzbox.Device{}
	if f.configured() {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if devices, err := f.devices(ctx); err == nil {
			for _, d := range devices {
				byAIN[d.AIN] = d
			}
		}
	}
	for _, pd := range p.Devices {
		d, ok := byAIN[pd.AIN]
		out := dev{Name: pd.Label, Value: "n/a", Colour: "hsl(0, 0%, 71%)", X: pd.X, Y: pd.Y, Known: ok}
		if ok {
			if out.Name == "" {
				out.Name = d.Name
			}
			// Power is the more interesting number when a device reports both.
			switch {
			case d.PowerW != nil:
				out.Value = fmt.Sprintf("%.0f", *d.PowerW)
				out.Unit = "W"
				out.Colour = "hsl(171, 100%, 41%)"
			case d.TempC != nil:
				out.Value = fmt.Sprintf("%.1f", *d.TempC)
				out.Unit = "\u00b0C"
				out.Colour = "hsl(229, 53%, 53%)"
			}
			if !d.Present {
				out.Colour = "hsl(348, 86%, 61%)"
			}
		}
		out.LabelY = pd.Y - 28
		out.ValueY = pd.Y + 5
		out.UnitY = pd.Y + 36
		data.Devices = append(data.Devices, out)
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

func firstPoint(points string) (float64, float64) {
	fields := strings.Fields(points)
	if len(fields) == 0 {
		return 0, 0
	}
	var x, y float64
	_, _ = fmt.Sscanf(fields[0], "%f,%f", &x, &y)
	return x, y
}

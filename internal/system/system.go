// Package system defines the contract every dragon-dash feature implements.
//
// A "system" is one top-navbar entry, Dragon, FritzHome, and whatever comes
// later. Systems are compiled into the binary and register themselves at init
// time; the config decides which ones are shown. There is no runtime plugin
// loading, deliberately: Go's plugin package cannot cross-compile and would
// cost us the single-binary property, which is the whole point.
//
// Everything the shell needs in order to draw itself, navbar entries, sidebar
// entries, settings fields, is returned as data. The shell renders it
// generically and knows nothing about any individual system.
package system

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"dragon-dash/web"
)

// NavItem is one entry in the left sidebar of a system.
// The first item a system returns is its default page.
type NavItem struct {
	Slug  string // URL segment: /s/<system>/<slug>
	Title string
}

// FieldKind decides which input the settings page renders.
type FieldKind string

const (
	KindText     FieldKind = "text"
	KindPassword FieldKind = "password"
	KindURL      FieldKind = "url"
	KindBool     FieldKind = "bool"
)

// ConfigField describes one setting. The settings page is generated from
// these, so a system never writes settings UI of its own. Key "metric_prefix"
// on system "fritzhome" is read from DD_SYSTEM_FRITZHOME_METRIC_PREFIX.
type ConfigField struct {
	Key     string // stored as system.<id>.<key>
	Label   string
	Help    string
	Kind    FieldKind
	Default string
	// Secret hides the value on the settings page. Set it for anything that
	// would be damaging to display on an unauthenticated page.
	Secret bool
}

// Store is the subset of configuration a system may read: its own keys.
// There is no setter. Configuration is read-only at runtime, which is what
// lets the app run on a LAN without a login to protect.
type Store interface {
	Get(key string) string
	GetOr(key, def string) string
	Bool(key string) bool
}

// Deps is what the shell hands a system at registration time.
type Deps struct {
	Config Store
	Log    *slog.Logger
	// PromURL is the configured Prometheus base URL, or "" if unset.
	PromURL func() string
}

// System is the contract. Keep it small and serialisable-ish: if out-of-process
// plugins ever become worth supporting, this is the seam they would go through.
type System interface {
	ID() string    // stable, URL-safe: "dragon"
	Title() string // navbar label: "Dragon"
	Nav() []NavItem
	ConfigSchema() []ConfigField

	// Render returns the page body for a sidebar slug. The shell wraps it in
	// the layout, so a system never emits <html> or navigation.
	Render(slug string, r *http.Request) (template.HTML, error)

	// Register is called once at startup. A system may attach its own
	// endpoints (htmx fragments, JSON for charts) under prefix, which is
	// always "/s/<id>/api/".
	Register(mux *http.ServeMux, prefix string, deps Deps)
}

var registry []System

// Register adds a system. Call it from an init function.
func Register(s System) { registry = append(registry, s) }

// All returns every compiled-in system, in registration order.
func All() []System { return registry }

// Metric is one sample for the /metrics endpoint.
type Metric struct {
	Name   string
	Help   string
	Type   string // "gauge" or "counter"
	Labels map[string]string
	Value  float64
}

// Collector is optional. A system that implements it also becomes a Prometheus
// target: the shell exposes /metrics and asks every enabled collector.
//
// This is what lets dragon-dash both gather and display the same data without
// a separate exporter process, while history still lives in Prometheus.
type Collector interface {
	Collect(ctx context.Context) ([]Metric, error)
}

// FuncMap is the template helper set shared by the shell and every system.
func FuncMap() template.FuncMap { return template.FuncMap{"dict": Dict} }

// Dict builds a map inline so a component can be called with named arguments:
//
//	{{template "notice" (dict "Kind" "danger" "Body" .Err)}}
func Dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict needs an even number of arguments, got %d", len(pairs))
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict key %d is not a string", i)
		}
		m[k] = pairs[i+1]
	}
	return m, nil
}

// Templates parses a system's own templates together with the shared
// components, so any system can use {{template "stat-card" .}} without
// redeclaring it.
func Templates(fsys fs.FS, patterns ...string) (*template.Template, error) {
	t, err := template.New("").Funcs(FuncMap()).
		ParseFS(web.Components, "templates/components.html")
	if err != nil {
		return nil, err
	}
	return t.ParseFS(fsys, patterns...)
}

// MustTemplates is Templates for use in Register, where a template error is a
// programming mistake rather than a runtime condition.
func MustTemplates(fsys fs.FS, patterns ...string) *template.Template {
	t, err := Templates(fsys, patterns...)
	if err != nil {
		panic(err)
	}
	return t
}

// PageTop is the infobar every page carries: a title, context items shown
// left and separated by pipes, and actions on the right. Building it in Go
// rather than in the template keeps the markup in one place.
type PageTop struct {
	Title   string
	Info    []template.HTML
	Actions []template.HTML
}

// Infof appends a formatted context item. The format string is trusted markup
// written by a system; interpolate user or device data with template.HTMLEscape
// first if it could contain markup.
func (p *PageTop) Infof(format string, args ...any) {
	p.Info = append(p.Info, template.HTML(fmt.Sprintf(format, args...)))
}

// Actionf appends a formatted action, rendered right-aligned in declaration
// order: state-changing buttons first, filters next, navigation last.
func (p *PageTop) Actionf(format string, args ...any) {
	p.Actions = append(p.Actions, template.HTML(fmt.Sprintf(format, args...)))
}

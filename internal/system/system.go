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

// Templates parses a system's own templates together with the shared
// components, so any system can use {{template "stat-card" .}} without
// redeclaring it.
func Templates(fsys fs.FS, patterns ...string) (*template.Template, error) {
	t, err := template.ParseFS(web.Components, "templates/components.html")
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

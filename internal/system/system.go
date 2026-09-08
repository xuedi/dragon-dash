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
	"html/template"
	"log/slog"
	"net/http"
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
// these, so a system never writes settings UI of its own.
type ConfigField struct {
	Key     string // stored as system.<id>.<key>
	Label   string
	Help    string
	Kind    FieldKind
	Default string
}

// Store is the subset of configuration a system may touch: its own keys.
type Store interface {
	Get(key string) string
	Set(key, value string) error
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

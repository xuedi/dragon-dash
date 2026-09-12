// Package server is the shell: routing, layout, settings.
//
// It knows nothing about any particular system. Navigation is assembled from
// what the systems declare, and the settings form is generated from their
// config schemas, so adding a system requires no change in this package.
package server

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"dragon-dash/internal/config"
	"dragon-dash/internal/system"
	"dragon-dash/internal/version"
	"dragon-dash/web"
)

const prometheusURLKey = "core.prometheus_url"

// themeCookie holds a light/dark override, or is absent to follow the browser.
//
// A cookie rather than localStorage because the page is server rendered: the
// theme arrives as an attribute in the HTML itself, so there is no moment where
// a dark page has already painted white and waits for a script to correct it.
// localStorage is invisible to the server and would need a blocking script in
// the head to avoid exactly that flash.
const themeCookie = "dd_theme"

// theme returns "light", "dark", or "" to follow the browser.
//
// The value is whitelisted rather than passed through: it ends up in an
// attribute, and a cookie is client-controlled input like any other.
func theme(r *http.Request) string {
	c, err := r.Cookie(themeCookie)
	if err != nil {
		return ""
	}
	switch c.Value {
	case "light", "dark":
		return c.Value
	}
	return ""
}

type Server struct {
	cfg   *config.Config
	log   *slog.Logger
	tmpl  *template.Template
	mux   *http.ServeMux
	files []string
}

func New(cfg *config.Config, log *slog.Logger, files []string) (*Server, error) {
	tmpl, err := template.New("").Funcs(system.FuncMap()).
		ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s := &Server{cfg: cfg, log: log, tmpl: tmpl, mux: http.NewServeMux(), files: files}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// PromURL is handed to systems so they always read the current value rather
// than a copy taken at startup.
func (s *Server) PromURL() string { return s.cfg.Get(prometheusURLKey) }

func (s *Server) routes() {
	s.mux.Handle("GET /static/", http.FileServerFS(web.Static))
	s.mux.HandleFunc("GET /{$}", s.handleRoot)
	s.mux.HandleFunc("GET /settings", s.handleSettings)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /s/{system}/{$}", s.handleSystemDefault)
	s.mux.HandleFunc("GET /s/{system}/{slug}", s.handleSystemPage)

	// Each system gets its own subtree for fragments and JSON.
	for _, sys := range system.All() {
		prefix := "/s/" + sys.ID() + "/api/"
		sys.Register(s.mux, prefix, system.Deps{
			Config:  s.cfg.Scoped(sys.ID()),
			Log:     s.log.With("system", sys.ID()),
			PromURL: s.PromURL,
		})
	}
}

// enabled returns the systems that should be visible right now.
func (s *Server) enabled() []system.System {
	var out []system.System
	for _, sys := range system.All() {
		if s.cfg.Enabled(sys.ID()) {
			out = append(out, sys)
		}
	}
	return out
}

func (s *Server) find(id string) system.System {
	for _, sys := range s.enabled() {
		if sys.ID() == id {
			return sys
		}
	}
	return nil
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if en := s.enabled(); len(en) > 0 {
		http.Redirect(w, r, "/s/"+en[0].ID()+"/", http.StatusFound)
		return
	}
	// Every system switched off: send the user somewhere useful rather than 404.
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSystemDefault(w http.ResponseWriter, r *http.Request) {
	sys := s.find(r.PathValue("system"))
	if sys == nil {
		http.NotFound(w, r)
		return
	}
	nav := sys.Nav()
	if len(nav) == 0 {
		http.Error(w, "system declares no pages", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/s/"+sys.ID()+"/"+nav[0].Slug, http.StatusFound)
}

func (s *Server) handleSystemPage(w http.ResponseWriter, r *http.Request) {
	sys := s.find(r.PathValue("system"))
	if sys == nil {
		http.NotFound(w, r)
		return
	}
	slug := r.PathValue("slug")

	var known bool
	for _, n := range sys.Nav() {
		if n.Slug == slug {
			known = true
			break
		}
	}
	if !known {
		http.NotFound(w, r)
		return
	}

	data := s.layout(r, sys, slug)
	body, err := sys.Render(slug, r)
	if err != nil {
		// Render the shell anyway: a broken page should not cost the user
		// their navigation, and the error belongs on screen, not only in logs.
		s.log.Error("system render failed", "system", sys.ID(), "slug", slug, "err", err)
		data.Error = err.Error()
	}
	data.Body = body
	s.render(w, data)
}

type navLink struct {
	Href, Title string
	Active      bool
}

type sysLink struct {
	ID, Title string
	Active    bool
}

type layoutData struct {
	Version        string
	Theme          string
	PageTitle      string
	ActiveTitle    string
	Systems        []sysLink
	Nav            []navLink
	SettingsActive bool
	Body           template.HTML
	Error          string
}

func (s *Server) layout(r *http.Request, active system.System, slug string) layoutData {
	d := layoutData{Version: version.Version, Theme: theme(r)}
	for _, sys := range s.enabled() {
		isActive := active != nil && sys.ID() == active.ID()
		d.Systems = append(d.Systems, sysLink{ID: sys.ID(), Title: sys.Title(), Active: isActive})
	}
	if active == nil {
		return d
	}
	d.ActiveTitle = active.Title()
	d.PageTitle = active.Title()
	for _, n := range active.Nav() {
		if n.Slug == slug {
			d.PageTitle = n.Title + " · " + active.Title()
		}
		d.Nav = append(d.Nav, navLink{
			Href:   "/s/" + active.ID() + "/" + n.Slug,
			Title:  n.Title,
			Active: n.Slug == slug,
		})
	}
	return d
}

func (s *Server) render(w http.ResponseWriter, d layoutData) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "layout", d); err != nil {
		s.log.Error("layout render failed", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// ---- settings -------------------------------------------------------------
//
// Read-only. Configuration comes from .env files and the environment, so this
// page reports what is in effect and where each value came from. There is no
// form to submit, which is why an unauthenticated LAN deployment is defensible.

type settingsField struct {
	Label, Help string
	EnvName     string
	Value       string
	Source      string
	Set         bool
}

type settingsSystem struct {
	ID, Title string
	Enabled   bool
	EnvName   string
	Fields    []settingsField
}

type settingsData struct {
	Core    []settingsField
	Systems []settingsSystem
	Files   []string
}

const redacted = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"

// coreField shows unset as the grey placeholder when one is given, otherwise as
// the "not set" warning.
func (s *Server) coreField(key, label, help, unset string) settingsField {
	v := s.cfg.Get(key)
	f := settingsField{
		Label:   label,
		Help:    help,
		EnvName: config.EnvName(key),
		Value:   v,
		Source:  s.cfg.Source(key),
		Set:     v != "",
	}
	if !f.Set {
		f.Value = unset
	}
	return f
}

func (s *Server) settingsData() settingsData {
	d := settingsData{
		Files: s.files,
		Core: []settingsField{
			s.coreField(prometheusURLKey, "Prometheus URL",
				"Every metric on every page is read from here.", ""),
			s.coreField(tlsCertKey, "TLS certificate",
				"HTTPS is on when both the certificate and the key are set.", "HTTPS is off"),
			s.coreField(tlsKeyKey, "TLS key",
				"Readable by the service user and root, nobody else.", "HTTPS is off"),
		},
	}
	for _, sys := range system.All() {
		enabledKey := "system." + sys.ID() + ".enabled"
		ss := settingsSystem{
			ID:      sys.ID(),
			Title:   sys.Title(),
			Enabled: s.cfg.Enabled(sys.ID()),
			EnvName: config.EnvName(enabledKey),
		}
		scope := s.cfg.Scoped(sys.ID())
		for _, f := range sys.ConfigSchema() {
			val := scope.Get(f.Key)
			set := val != ""
			if f.Secret && set {
				val = redacted
			}
			if !set && f.Default != "" {
				val = f.Default + "  (default)"
			}
			ss.Fields = append(ss.Fields, settingsField{
				Label:   f.Label,
				Help:    f.Help,
				EnvName: scope.EnvName(f.Key),
				Value:   val,
				Source:  scope.Source(f.Key),
				Set:     set,
			})
		}
		d.Systems = append(d.Systems, ss)
	}
	return d
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, "settings", s.settingsData()); err != nil {
		s.log.Error("settings render failed", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	d := s.layout(r, nil, "")
	d.PageTitle = "Settings"
	d.SettingsActive = true
	d.Body = template.HTML(body.String())
	s.render(w, d)
}

// contextWithTimeout keeps the metrics handler readable.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

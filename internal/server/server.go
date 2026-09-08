// Package server is the shell: routing, layout, settings.
//
// It knows nothing about any particular system. Navigation is assembled from
// what the systems declare, and the settings form is generated from their
// config schemas, so adding a system requires no change in this package.
package server

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"dragon-dash/internal/config"
	"dragon-dash/internal/system"
	"dragon-dash/internal/version"
	"dragon-dash/web"
)

const prometheusURLKey = "core.prometheus_url"

type Server struct {
	cfg  *config.Config
	log  *slog.Logger
	tmpl *template.Template
	mux  *http.ServeMux
}

func New(cfg *config.Config, log *slog.Logger) (*Server, error) {
	tmpl, err := template.ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s := &Server{cfg: cfg, log: log, tmpl: tmpl, mux: http.NewServeMux()}
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
	s.mux.HandleFunc("POST /settings", s.handleSettingsSave)
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

	data := s.layout(sys, slug)
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
	PageTitle      string
	ActiveTitle    string
	Systems        []sysLink
	Nav            []navLink
	SettingsActive bool
	Body           template.HTML
	Error          string
}

func (s *Server) layout(active system.System, slug string) layoutData {
	d := layoutData{Version: version.Version}
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

type settingsField struct {
	Name, Label, Help string
	Kind, InputType   string
	Value, Default    string
	Checked           bool
}

type settingsSystem struct {
	ID, Title string
	Enabled   bool
	Fields    []settingsField
}

type settingsData struct {
	PrometheusURL string
	Systems       []settingsSystem
	Saved         bool
}

func (s *Server) settingsData(saved bool) settingsData {
	d := settingsData{PrometheusURL: s.cfg.Get(prometheusURLKey), Saved: saved}
	for _, sys := range system.All() { // all, not just enabled: you need to switch them back on
		ss := settingsSystem{ID: sys.ID(), Title: sys.Title(), Enabled: s.cfg.Enabled(sys.ID())}
		scope := s.cfg.Scoped(sys.ID())
		for _, f := range sys.ConfigSchema() {
			ss.Fields = append(ss.Fields, settingsField{
				Name:      scope.Prefix() + f.Key,
				Label:     f.Label,
				Help:      f.Help,
				Kind:      string(f.Kind),
				InputType: inputType(f.Kind),
				Value:     scope.Get(f.Key),
				Default:   f.Default,
				Checked:   scope.Bool(f.Key),
			})
		}
		d.Systems = append(d.Systems, ss)
	}
	return d
}

func inputType(k system.FieldKind) string {
	switch k {
	case system.KindPassword:
		return "password"
	case system.KindURL:
		return "url"
	default:
		return "text"
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, false)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	if err := s.cfg.Set(prometheusURLKey, strings.TrimSpace(r.FormValue(prometheusURLKey))); err != nil {
		s.log.Error("saving prometheus url", "err", err)
	}

	for _, sys := range system.All() {
		// Unchecked checkboxes are simply absent from the form body, which is
		// why enablement is derived from presence rather than from a value.
		on := r.Form.Has("enabled." + sys.ID())
		if err := s.cfg.SetEnabled(sys.ID(), on); err != nil {
			s.log.Error("saving enabled flag", "system", sys.ID(), "err", err)
		}

		scope := s.cfg.Scoped(sys.ID())
		for _, f := range sys.ConfigSchema() {
			name := scope.Prefix() + f.Key
			var val string
			if f.Kind == system.KindBool {
				if r.Form.Has(name) {
					val = "1"
				} else {
					val = "0"
				}
			} else {
				val = strings.TrimSpace(r.FormValue(name))
			}
			if err := scope.Set(f.Key, val); err != nil {
				s.log.Error("saving setting", "key", name, "err", err)
			}
		}
	}
	s.renderSettings(w, true)
}

func (s *Server) renderSettings(w http.ResponseWriter, saved bool) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, "settings", s.settingsData(saved)); err != nil {
		s.log.Error("settings render failed", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	d := s.layout(nil, "")
	d.PageTitle = "Settings"
	d.SettingsActive = true
	d.Body = template.HTML(body.String())
	s.render(w, d)
}

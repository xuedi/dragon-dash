package server

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"armdash/internal/auth"
	"armdash/internal/config"
	"armdash/internal/system"
)

const (
	authUserKey   = "core.auth_user"
	authHashKey   = "core.auth_password_hash"
	sessionCookie = "dd_session"

	maxFailures = 10
	failWindow  = 15 * time.Minute
	lockFor     = 15 * time.Minute
)

// login is the one account that may change things. It is nil when none is
// configured, and then nothing can be changed at all.
type login struct {
	user, hash string
	sessions   *auth.Sessions
	limiter    *auth.Limiter
}

func newLogin(cfg *config.Config) (*login, error) {
	user, hash := cfg.Get(authUserKey), cfg.Get(authHashKey)
	if user == "" && hash == "" {
		return nil, nil
	}
	if user == "" || hash == "" {
		return nil, fmt.Errorf("%s and %s must be set together",
			config.EnvName(authUserKey), config.EnvName(authHashKey))
	}
	if err := auth.Check(hash); err != nil {
		return nil, fmt.Errorf("%s: %w", config.EnvName(authHashKey), err)
	}
	return &login{
		user:     user,
		hash:     hash,
		sessions: auth.NewSessions(auth.SessionTTL),
		limiter:  auth.NewLimiter(maxFailures, failWindow, lockFor),
	}, nil
}

// withSession marks the request for system.CanEdit when it carries a live
// session. Every request passes through here, pages and API alike.
func (s *Server) withSession(r *http.Request) *http.Request {
	if s.login == nil {
		return r
	}
	if c, err := r.Cookie(sessionCookie); err == nil && s.login.sessions.Valid(c.Value) {
		return system.AllowEdit(r)
	}
	return r
}

// requireLogin sends a visitor who is not logged in to the login page and
// reports whether it did. Without a configured login there is nobody to ask
// for, and the page stays as open as it always was.
func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if s.login == nil || system.CanEdit(r) {
		return false
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
	return true
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// refuse answers a write that is not allowed. htmx gets the notice component,
// so an upload form shows why in its message area; anything else plain text.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, code int, title, body string) {
	if r.Header.Get("HX-Request") == "true" {
		var buf bytes.Buffer
		err := s.tmpl.ExecuteTemplate(&buf, "notice", map[string]any{"Kind": "danger", "Title": title, "Body": body})
		if err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code)
			_, _ = buf.WriteTo(w)
			return
		}
	}
	http.Error(w, title+": "+body, code)
}

// localPath keeps the return address after a login on this site. "//host" and
// "/\host" are read by browsers as another host, and they drop tabs and
// newlines before parsing, so "/\t/host" is one too.
func localPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") ||
		strings.HasPrefix(next, "/login") ||
		strings.ContainsFunc(next, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

// clientAddr is the peer address. X-Forwarded-For is never read: armdash
// terminates TLS itself, and a header anyone can set would let a guesser pick
// a fresh address for every attempt.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginData struct {
	Configured      bool
	Next, User, Err string
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, code int, d loginData) {
	d.Configured = s.login != nil
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, "login", d); err != nil {
		s.log.Error("login render failed", "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	l := s.layout(r, nil, "")
	l.PageTitle = "Log in"
	l.LoginActive = true
	l.Here = d.Next
	l.Body = template.HTML(body.String())
	s.renderCode(w, code, l)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	next := localPath(r.URL.Query().Get("next"))
	if system.CanEdit(r) {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	s.renderLogin(w, r, http.StatusOK, loginData{Next: next})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.login == nil {
		s.renderLogin(w, r, http.StatusForbidden, loginData{})
		return
	}
	addr := clientAddr(r)
	d := loginData{Next: localPath(r.PostFormValue("next")), User: r.PostFormValue("user")}
	if ok, wait := s.login.limiter.Allowed(addr); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		d.Err = fmt.Sprintf("Too many failed attempts. Try again in %d minutes.", int(wait.Minutes())+1)
		s.renderLogin(w, r, http.StatusTooManyRequests, d)
		return
	}

	// One password check against the one configured hash, whatever name was
	// typed, so the time an attempt takes does not tell whether the name was
	// right.
	nameOK := subtle.ConstantTimeCompare([]byte(d.User), []byte(s.login.user)) == 1
	passOK := auth.Verify(r.PostFormValue("password"), s.login.hash)
	if !nameOK || !passOK {
		locked := s.login.limiter.Fail(addr)
		s.log.Warn("login failed", "addr", addr, "locked", locked)
		d.Err = "Wrong user name or password."
		s.renderLogin(w, r, http.StatusUnauthorized, d)
		return
	}

	s.login.limiter.Reset(addr)
	id, err := s.login.sessions.Create()
	if err != nil {
		s.log.Error("creating a session", "err", err)
		http.Error(w, "could not create a session", http.StatusInternalServerError)
		return
	}
	s.setSession(w, r, id, int(auth.SessionTTL.Seconds()))
	s.log.Info("logged in", "addr", addr)
	http.Redirect(w, r, d.Next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && s.login != nil {
		s.login.sessions.Delete(c.Value)
	}
	s.setSession(w, r, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setSession marks the cookie Secure only when the request came over TLS: on
// a plain-HTTP LAN install a Secure cookie would never be stored at all.
func (s *Server) setSession(w http.ResponseWriter, r *http.Request, id string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"dragon-dash/internal/config"
)

// The cookie is client controlled and its value lands in an attribute on the
// html element, so anything that is not one of the two known schemes has to
// fall back to following the browser.
func TestThemeWhitelistsCookieValue(t *testing.T) {
	cases := []struct {
		cookie string
		want   string
	}{
		{"dark", "dark"},
		{"light", "light"},
		{"", ""},
		{"DARK", ""},
		{"auto", ""},
		{`"><script>alert(1)</script>`, ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: themeCookie, Value: c.cookie})
		if got := theme(r); got != c.want {
			t.Errorf("theme(%q) = %q, want %q", c.cookie, got, c.want)
		}
	}
}

func TestThemeWithoutCookieFollowsBrowser(t *testing.T) {
	if got := theme(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("theme() = %q with no cookie, want the empty follow-the-browser value", got)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Prometheus scrapes the plain listener over loopback. Redirecting it would
// send the scrape off to a certificate it does not trust.
func TestPlainHandlerKeepsMetricsOnHTTP(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).PlainHandler(":443").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics on the plain listener = %d, want 200", rec.Code)
	}
}

func TestPlainHandlerRedirectsToHTTPS(t *testing.T) {
	cases := []struct {
		tlsAddr, url, want string
	}{
		{":443", "http://dragon/", "https://dragon/"},
		{":443", "http://dragon:80/settings", "https://dragon/settings"},
		{":443", "http://dragon/s/dragon/overview?range=7d", "https://dragon/s/dragon/overview?range=7d"},
		{"127.0.0.1:9495", "http://127.0.0.1:9494/", "https://127.0.0.1:9495/"},
		{":443", "http://[::1]/", "https://[::1]/"},
		{"[::1]:9495", "http://[::1]:9494/x", "https://[::1]:9495/x"},
	}
	s := newTestServer(t)
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.PlainHandler(c.tlsAddr).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.url, nil))
		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("%s via %s = %d, want 301", c.url, c.tlsAddr, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != c.want {
			t.Errorf("%s via %s redirects to %q, want %q", c.url, c.tlsAddr, got, c.want)
		}
		if h := rec.Header().Get("Strict-Transport-Security"); h != "" {
			t.Errorf("%s sent Strict-Transport-Security %q, want none", c.url, h)
		}
	}
}

func TestTLSFilesMustBeSetTogether(t *testing.T) {
	cases := []struct {
		cert, key string
		wantErr   bool
	}{
		{"", "", false},
		{"/tls/dd.crt", "/tls/dd.key", false},
		{"/tls/dd.crt", "", true},
		{"", "/tls/dd.key", true},
	}
	for _, c := range cases {
		t.Setenv("DD_CORE_TLS_CERT", c.cert)
		t.Setenv("DD_CORE_TLS_KEY", c.key)
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		cert, key, err := TLSFiles(cfg)
		if (err != nil) != c.wantErr {
			t.Errorf("cert=%q key=%q: err = %v, want error %v", c.cert, c.key, err, c.wantErr)
			continue
		}
		if !c.wantErr && (cert != c.cert || key != c.key) {
			t.Errorf("cert=%q key=%q: got %q, %q", c.cert, c.key, cert, key)
		}
	}
}

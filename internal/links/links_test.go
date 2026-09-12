package links

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dragon-dash/internal/config"
)

func load(t *testing.T, env map[string]string) *config.Config {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestParseKeepsListOrderAndDefaults(t *testing.T) {
	ls, err := Parse(load(t, map[string]string{
		"DD_LINKS":            "wiki, grafana",
		"DD_LINK_WIKI_TITLE":  "Wiki",
		"DD_LINK_WIKI_URL":    "http://127.0.0.1:8081",
		"DD_LINK_WIKI_MODE":   "proxy",
		"DD_LINK_GRAFANA_URL": "http://dragon:3000/",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 2 {
		t.Fatalf("got %d links, want 2", len(ls))
	}
	if l := ls[0]; l.ID != "wiki" || l.Title != "Wiki" || l.Mode != ModeProxy {
		t.Errorf("first link = %+v", l)
	}
	if l := ls[1]; l.ID != "grafana" || l.Title != "grafana" || l.Mode != ModeFrame {
		t.Errorf("second link = %+v, want the id as title and frame mode", l)
	}
}

func TestNoLinksIsNotAnError(t *testing.T) {
	ls, err := Parse(load(t, nil))
	if err != nil || len(ls) != 0 {
		t.Fatalf("got %v, %v; want no links and no error", ls, err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing url":    {"DD_LINKS": "wiki"},
		"hyphen in id":   {"DD_LINKS": "my-wiki", "DD_LINK_MY_WIKI_URL": "http://x"},
		"uppercase id":   {"DD_LINKS": "Wiki", "DD_LINK_WIKI_URL": "http://x"},
		"duplicate id":   {"DD_LINKS": "wiki,wiki", "DD_LINK_WIKI_URL": "http://x"},
		"unknown mode":   {"DD_LINKS": "wiki", "DD_LINK_WIKI_URL": "http://x", "DD_LINK_WIKI_MODE": "iframe"},
		"script url":     {"DD_LINKS": "wiki", "DD_LINK_WIKI_URL": "javascript:alert(1)"},
		"relative url":   {"DD_LINKS": "wiki", "DD_LINK_WIKI_URL": "/wiki"},
		"credentials":    {"DD_LINKS": "wiki", "DD_LINK_WIKI_URL": "http://admin:pw@127.0.0.1:8081"},
		"unlisted link":  {"DD_LINK_WIKI_URL": "http://x"},
		"misspelt field": {"DD_LINKS": "wiki", "DD_LINK_WIKI_URL": "http://x", "DD_LINK_WIKI_TITEL": "Wiki"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(load(t, env)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestSrc(t *testing.T) {
	u, _ := url.Parse("http://dragon:3000/")
	framed := Link{ID: "grafana", URL: u, Mode: ModeFrame}
	proxied := Link{ID: "wiki", URL: u, Mode: ModeProxy}
	if got := framed.Src("ignored", "a=b"); got != "http://dragon:3000/" {
		t.Errorf("framed src = %q", got)
	}
	if got := proxied.Src("", ""); got != "/x/wiki/" {
		t.Errorf("proxied root src = %q", got)
	}
	if got := proxied.Src("doku.php", "id=start"); got != "/x/wiki/doku.php?id=start" {
		t.Errorf("proxied deep src = %q", got)
	}
}

func proxyTo(t *testing.T, h http.HandlerFunc, path string) Link {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	u, err := url.Parse(up.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	return Link{ID: "wiki", Title: "Wiki", URL: u, Mode: ModeProxy}
}

func TestProxyTellsUpstreamTheRealURL(t *testing.T) {
	var host, path, query, body string
	var hdr http.Header
	l := proxyTo(t, func(w http.ResponseWriter, r *http.Request) {
		host, path, query, hdr = r.Host, r.URL.Path, r.URL.RawQuery, r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}, "/dokuwiki")

	req := httptest.NewRequest(http.MethodPost, "http://dragon/x/wiki/doku.php?id=start", strings.NewReader("wikitext=hi"))
	req.RemoteAddr = "192.168.178.20:5555"
	// A visitor must not be able to claim a different client or mount point.
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Forwarded-Prefix", "/elsewhere")
	rec := httptest.NewRecorder()
	l.Handler(discard).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for name, c := range map[string]struct{ got, want string }{
		"path":               {path, "/dokuwiki/doku.php"},
		"query":              {query, "id=start"},
		"body":               {body, "wikitext=hi"},
		"Host":               {host, "dragon"},
		"X-Forwarded-Host":   {hdr.Get("X-Forwarded-Host"), "dragon"},
		"X-Forwarded-Proto":  {hdr.Get("X-Forwarded-Proto"), "http"},
		"X-Forwarded-For":    {hdr.Get("X-Forwarded-For"), "192.168.178.20"},
		"X-Forwarded-Prefix": {hdr.Get("X-Forwarded-Prefix"), "/x/wiki"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
}

func TestProxyReportsHTTPSWhenServedOverTLS(t *testing.T) {
	var proto string
	l := proxyTo(t, func(w http.ResponseWriter, r *http.Request) { proto = r.Header.Get("X-Forwarded-Proto") }, "")
	req := httptest.NewRequest(http.MethodGet, "https://dragon/x/wiki/", nil)
	req.TLS = &tls.ConnectionState{}
	l.Handler(discard).ServeHTTP(httptest.NewRecorder(), req)
	if proto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", proto)
	}
}

func TestProxyRewritesUpstreamRedirect(t *testing.T) {
	var l Link
	l = proxyTo(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, l.URL.String()+"/doku.php?id=saved", http.StatusSeeOther)
	}, "")
	rec := httptest.NewRecorder()
	l.Handler(discard).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://dragon/x/wiki/doku.php", nil))
	if got := rec.Header().Get("Location"); got != "/x/wiki/doku.php?id=saved" {
		t.Errorf("Location = %q, want it under /x/wiki", got)
	}
}

func TestRewriteLocation(t *testing.T) {
	cases := []struct {
		target, loc, want string
	}{
		{"http://127.0.0.1:8081", "http://127.0.0.1:8081/doku.php?id=a", "/x/wiki/doku.php?id=a"},
		{"http://127.0.0.1:8081/", "http://127.0.0.1:8081/", "/x/wiki/"},
		{"http://127.0.0.1:8081/dokuwiki", "http://127.0.0.1:8081/dokuwiki/doku.php", "/x/wiki/doku.php"},
		{"http://127.0.0.1:8081/dokuwiki", "http://127.0.0.1:8081/dokuwiki", "/x/wiki/"},
		{"http://127.0.0.1:8081/dokuwiki", "http://127.0.0.1:8081/dokuwikis/x", ""},
		{"http://127.0.0.1:8081", "https://127.0.0.1:8081/doku.php", ""},
		{"http://127.0.0.1:8081", "http://dragon/x/wiki/doku.php", ""},
		{"http://127.0.0.1:8081", "/x/wiki/doku.php", ""},
		{"http://127.0.0.1:8081", "", ""},
	}
	for _, c := range cases {
		target, _ := url.Parse(c.target)
		if got := rewriteLocation(c.loc, target, "/x/wiki"); got != c.want {
			t.Errorf("target %s, Location %q: got %q, want %q", c.target, c.loc, got, c.want)
		}
	}
}

func TestProxyAnswersBadGatewayWhenUpstreamIsDown(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(up.URL)
	up.Close()
	l := Link{ID: "wiki", Title: "Wiki", URL: u, Mode: ModeProxy}
	rec := httptest.NewRecorder()
	l.Handler(discard).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://dragon/x/wiki/", nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

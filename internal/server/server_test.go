package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

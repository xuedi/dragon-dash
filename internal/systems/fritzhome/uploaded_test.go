package fritzhome

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngFile(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInspectUpload(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		ct   string
		w, h float64
		err  string
	}{
		{"png", pngFile(t, 200, 100), "image/png", 200, 100, ""},
		{"svg with a viewBox", []byte("<?xml version=\"1.0\"?>\n<!-- by hand -->\n<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"100%\" viewBox=\"0,0 400 300\"><rect/></svg>"), "image/svg+xml", 400, 300, ""},
		{"svg with a size", []byte(`<svg width="210mm" height="297mm"></svg>`), "image/svg+xml", 210, 297, ""},
		{"svg without a size", []byte(`<svg width="100%"></svg>`), "", 0, 0, "neither a viewBox"},
		{"svg with an infinite size", []byte(`<svg viewBox="0 0 Inf 1"></svg>`), "", 0, 0, "neither a viewBox"},
		{"html", []byte(`<html><body>hi</body></html>`), "", 0, 0, "not a SweetHome3D file"},
		{"json", []byte(`{"width": 800}`), "", 0, 0, "not a SweetHome3D file"},
		{"pdf", []byte("%PDF-1.4\n%binary"), "", 0, 0, "not a SweetHome3D file"},
		{"broken png", []byte("\x89PNG\r\n\x1a\ngarbage"), "", 0, 0, "cannot be read"},
		{"old drawing", sh3dFile(t, "<home/>"), "", 0, 0, "no walls or rooms"},
	}
	for _, c := range cases {
		u, err := inspectUpload(bytes.NewReader(c.body), int64(len(c.body)))
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if u.ContentType != c.ct || u.W != c.w || u.H != c.h || u.Home != nil {
			t.Errorf("%s: %+v, want %s %vx%v", c.name, u, c.ct, c.w, c.h)
		}
	}

	b := sh3dFile(t, testHome)
	if u, err := inspectUpload(bytes.NewReader(b), int64(len(b))); err != nil || u.Home == nil {
		t.Errorf("drawing: %+v, %v", u, err)
	}
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestPictureBecomesThePlan(t *testing.T) {
	f, mux := newTestFritz(t, t.TempDir(), "")
	pic := pngFile(t, 400, 200)
	post(mux, "/s/fritzhome/api/positions", `{"116570593149": {"x": 5000, "y": 5000}, "139790212573": {"x": 100, "y": 50}}`)
	if rec := upload(mux, pic); rec.Code != http.StatusOK {
		t.Fatalf("picture upload = %d %q", rec.Code, rec.Body.String())
	}

	html := page(t, f)
	for _, want := range []string{
		`<image href="/s/fritzhome/api/floorplan?v=`,
		`viewBox="0 0 1000 500"`,
		`class="fp-device" data-ain="139790212573"`,
		`class="fp-device fp-edit-only" data-ain="116570593149"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %s", want)
		}
	}

	rec := get(mux, "/s/fritzhome/api/floorplan")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pic) {
		t.Fatalf("picture = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want a sandbox for an SVG opened on its own", csp)
	}

	upload(mux, []byte(`<svg viewBox="0 0 300 600"><script>alert(1)</script></svg>`))
	if rec := get(mux, "/s/fritzhome/api/floorplan"); rec.Header().Get("Content-Type") != "image/svg+xml" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("svg served as %q, nosniff %q", rec.Header().Get("Content-Type"), rec.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(page(t, f), `viewBox="0 0 500 1000"`) {
		t.Error("a tall SVG should be scaled to 1000 on its long side")
	}

	upload(mux, sh3dFile(t, testHome))
	if rec := get(mux, "/s/fritzhome/api/floorplan"); rec.Code != http.StatusNotFound {
		t.Errorf("a drawing is served as a picture: %d", rec.Code)
	}
}

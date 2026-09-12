package fritzhome

import (
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"armdash/internal/sh3d"
)

var errUnsupported = errors.New("not a SweetHome3D file, an SVG, or a PNG, JPEG or GIF image")

// uploadInfo is what an uploaded plan turned out to be: a SweetHome3D drawing,
// or a picture shown as the whole plan.
type uploadInfo struct {
	Home        *sh3d.Home // set for a drawing
	ContentType string     // set for a picture
	W, H        float64
}

// inspectUpload decides what a file is by its content, never its name. Only
// the header of a picture is read, the browser does the decoding.
func inspectUpload(r io.ReaderAt, size int64) (*uploadInfo, error) {
	head := make([]byte, 512)
	n, _ := r.ReadAt(head, 0)
	switch ct := http.DetectContentType(head[:n]); {
	case ct == "application/zip":
		h, err := sh3d.Read(r, size)
		if err != nil {
			return nil, err
		}
		h = h.FirstLevel()
		if len(h.Walls)+len(h.Rooms) == 0 {
			return nil, errors.New("the drawing has no walls or rooms")
		}
		return &uploadInfo{Home: h}, nil
	case ct == "image/png", ct == "image/jpeg", ct == "image/gif":
		cfg, _, err := image.DecodeConfig(io.NewSectionReader(r, 0, size))
		if err != nil {
			return nil, fmt.Errorf("the image cannot be read: %w", err)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 {
			return nil, errors.New("the image is empty")
		}
		return &uploadInfo{ContentType: ct, W: float64(cfg.Width), H: float64(cfg.Height)}, nil
	case strings.HasPrefix(ct, "text/"):
		w, h, err := svgSize(io.NewSectionReader(r, 0, size))
		if err != nil {
			return nil, err
		}
		return &uploadInfo{ContentType: "image/svg+xml", W: w, H: h}, nil
	}
	return nil, errUnsupported
}

// svgSize reads the root element's viewBox, or failing that its width and
// height. Only the ratio matters, the plan is scaled anyway.
func svgSize(r io.Reader) (float64, float64, error) {
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, errUnsupported
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "svg" {
			return 0, 0, errUnsupported
		}
		a := map[string]string{}
		for _, at := range se.Attr {
			a[at.Name.Local] = at.Value
		}
		sep := func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' }
		if f := strings.FieldsFunc(a["viewBox"], sep); len(f) == 4 {
			w, _ := strconv.ParseFloat(f[2], 64)
			h, _ := strconv.ParseFloat(f[3], 64)
			if positive(w) && positive(h) {
				return w, h, nil
			}
		}
		if w, h := svgLength(a["width"]), svgLength(a["height"]); positive(w) && positive(h) {
			return w, h, nil
		}
		return 0, 0, errors.New("the SVG has neither a viewBox nor a width and height")
	}
}

// svgLength drops a unit ("210mm", "800px"). A percentage says nothing about
// the drawing's own size.
func svgLength(s string) float64 {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimRight(s, "abcdefghijklmnopqrstuvwxyz"), 64)
	if err != nil {
		return 0
	}
	return v
}

func positive(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

func (f *FritzHome) planFromUpload(path string, st os.FileInfo) (*view, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	u, err := inspectUpload(fh, st.Size())
	if err != nil {
		return nil, fmt.Errorf("the uploaded plan: %w", err)
	}
	if u.Home != nil {
		return planFromSH3D(u.Home), nil
	}
	// The modification time makes every upload a new URL, so no browser keeps
	// showing the old picture out of its cache.
	src := f.prefix + "floorplan?v=" + strconv.FormatInt(st.ModTime().UnixNano(), 36)
	return planFromImage(src, u.W, u.H), nil
}

// planFromImage shows an uploaded picture as the whole plan. Photos run to
// thousands of pixels and hand-made SVGs to hundreds, so the long side is
// scaled to 1000 units, which keeps markers and labels the same size on either.
func planFromImage(src string, w, h float64) *view {
	s := 1000 / max(w, h)
	return &view{Image: src, W: r2(w * s), H: r2(h * s)}
}

// handleImage serves an uploaded picture back to the page.
func (f *FritzHome) handleImage(w http.ResponseWriter, r *http.Request) {
	if f.dataDir == "" {
		http.NotFound(w, r)
		return
	}
	fh, err := os.Open(filepath.Join(f.dataDir, uploadName))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := inspectUpload(fh, st.Size())
	if err != nil || u.Home != nil {
		http.NotFound(w, r)
		return
	}
	// An SVG can carry script. Drawn through <image> it never runs, but the URL
	// can also be opened on its own, and then this policy is all that stops it.
	w.Header().Set("Content-Type", u.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; sandbox")
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

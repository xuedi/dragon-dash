package fritzhome

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
)

// The two files people change through the page. Configuration stays read-only;
// these are data, kept in the system's own data directory.
const (
	uploadName    = "floorplan" // a drawing or a picture, told apart by content
	positionsName = "positions.json"

	maxUpload    = 64 << 20
	maxPositions = 1 << 20
)

type spot struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (f *FritzHome) handleUpload(w http.ResponseWriter, r *http.Request) {
	if f.dataDir == "" {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := f.receivePlan(r); err != nil {
		f.deps.Log.Warn("floor plan upload rejected", "err", err)
		f.reject(w, err)
		return
	}
	f.deps.Log.Info("floor plan uploaded")
	// A new plan can move everything on the page, so htmx reloads it whole.
	w.Header().Set("HX-Refresh", "true")
}

// reject answers 422 with a notice for the page. htmx swaps only 2xx responses
// by default; the upload form opts this status in.
func (f *FritzHome) reject(w http.ResponseWriter, err error) {
	body, xerr := f.exec("notice", map[string]any{"Kind": "danger", "Title": "Upload rejected", "Body": err.Error()})
	if xerr != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = io.WriteString(w, string(body))
}

// receivePlan streams the upload straight into a temp file next to the plan,
// so nothing large is held in memory and nothing lands outside the data
// directory, then checks it is a plan the page can show before it replaces one.
func (f *FritzHome) receivePlan(r *http.Request) error {
	mr, err := r.MultipartReader()
	if err != nil {
		return errors.New("expected a file upload")
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return errors.New("no file was chosen")
		}
		if err != nil {
			return uploadErr(err)
		}
		if part.FormName() != "plan" {
			continue
		}
		return f.writeAtomic(uploadName, func(tmp *os.File) error {
			n, err := io.Copy(tmp, part)
			if err != nil {
				return uploadErr(err)
			}
			if _, err := inspectUpload(tmp, n); err != nil {
				return err
			}
			return f.keepPrevious(uploadName)
		})
	}
}

func uploadErr(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return fmt.Errorf("the file is larger than %d MB", maxUpload>>20)
	}
	return fmt.Errorf("reading the upload: %w", err)
}

// keepPrevious hard-links the current file to name.prev, so one bad upload is
// a rename away from undone. It runs before the new file is renamed over the
// old one, which a hard link survives.
func (f *FritzHome) keepPrevious(name string) error {
	cur := filepath.Join(f.dataDir, name)
	prev := cur + ".prev"
	if err := os.Remove(prev); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(cur, prev); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// writeAtomic fills a temp file in the data directory and renames it over
// name, so a reader sees the old file or the new one, never half of either.
// fill runs under the write lock and can veto the write by returning an error.
func (f *FritzHome) writeAtomic(name string, fill func(*os.File) error) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	if err := os.MkdirAll(f.dataDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.dataDir, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := fill(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(f.dataDir, name))
}

// savedPositions is nil when nothing has been saved yet, which is different
// from an empty map: that means every device was taken off the plan.
func (f *FritzHome) savedPositions() (map[string]spot, error) {
	if f.dataDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(f.dataDir, positionsName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]spot
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", positionsName, err)
	}
	if m == nil {
		m = map[string]spot{}
	}
	return m, nil
}

func (f *FritzHome) handlePositions(w http.ResponseWriter, r *http.Request) {
	if f.dataDir == "" {
		http.NotFound(w, r)
		return
	}
	var m map[string]spot
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPositions)).Decode(&m); err != nil || m == nil {
		http.Error(w, `expected a JSON object mapping each AIN to {"x": ..., "y": ...}`, http.StatusBadRequest)
		return
	}
	for ain, s := range m {
		if !validAIN(ain) {
			http.Error(w, fmt.Sprintf("%q is not an AIN", ain), http.StatusBadRequest)
			return
		}
		// JSON cannot carry NaN or infinity, so a range is all there is to check.
		if math.Abs(s.X) > 1e6 || math.Abs(s.Y) > 1e6 {
			http.Error(w, fmt.Sprintf("the position of %s is out of range", ain), http.StatusBadRequest)
			return
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = f.writeAtomic(positionsName, func(tmp *os.File) error {
		_, err := tmp.Write(append(b, '\n'))
		return err
	})
	if err != nil {
		f.deps.Log.Error("saving device positions", "err", err)
		http.Error(w, "could not save the positions", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validAIN accepts what the box uses as an identifier: digits, plus letters and
// a dash for groups, templates and the sub-units of one physical device.
func validAIN(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return true
}

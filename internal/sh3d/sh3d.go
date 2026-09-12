// Package sh3d reads plan geometry out of a SweetHome3D file.
//
// A .sh3d file is a ZIP archive. Since SweetHome3D 5.3 it carries a Home.xml
// entry next to the Java-serialised Home, and that XML is the only part read
// here: walls, rooms, levels and furniture as plain numbers. Models, textures
// and everything 3D are ignored. Coordinates are centimetres with y pointing
// down, angles are radians clockwise.
package sh3d

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
)

// MaxXML caps Home.xml once decompressed. A real flat is tens of kilobytes; the
// cap is what stops a small archive from inflating into gigabytes.
const MaxXML = 16 << 20

type Point struct{ X, Y float64 }

type Level struct {
	ID             string
	Name           string
	Elevation      float64
	ElevationIndex int
}

type Wall struct {
	Level                      string
	XStart, YStart, XEnd, YEnd float64
	Thickness                  float64
}

type Room struct {
	Level                    string
	Name                     string
	Points                   []Point
	NameXOffset, NameYOffset float64
}

// Piece is any piece of furniture, doors and windows included. X and Y are the
// centre of its footprint, Width runs along the angle and Depth across it.
type Piece struct {
	Level              string
	Name               string
	X, Y, Width, Depth float64
	Angle, Elevation   float64
	DoorOrWindow       bool
	WallThickness      float64 // fraction of Depth that sits inside the wall
	WallDistance       float64 // fraction of Depth in front of the wall
}

type Home struct {
	Levels    []Level
	Walls     []Wall
	Rooms     []Room
	Furniture []Piece
}

// Open reads a .sh3d file from disk.
func Open(path string) (*Home, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return Read(f, st.Size())
}

// Read decodes a .sh3d archive. It takes a ReaderAt so an upload can be checked
// before it replaces anything on disk.
func Read(r io.ReaderAt, size int64) (*Home, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, errors.New("not a SweetHome3D file: it is not a ZIP archive")
	}
	for _, f := range zr.File {
		if f.Name != "Home.xml" {
			continue
		}
		if f.UncompressedSize64 > MaxXML {
			return nil, fmt.Errorf("Home.xml is %d bytes, more than the %d allowed", f.UncompressedSize64, MaxXML)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening Home.xml: %w", err)
		}
		defer rc.Close()
		// The header can lie about the size, so the limit is enforced on the
		// bytes actually inflated as well.
		b, err := io.ReadAll(io.LimitReader(rc, MaxXML+1))
		if err != nil {
			return nil, fmt.Errorf("reading Home.xml: %w", err)
		}
		if len(b) > MaxXML {
			return nil, fmt.Errorf("Home.xml is larger than the %d bytes allowed", MaxXML)
		}
		return decode(b)
	}
	return nil, errors.New("the file has no Home.xml; re-save it with SweetHome3D 5.3 or newer")
}

var furnitureTags = map[string]bool{
	"pieceOfFurniture": true,
	"doorOrWindow":     true,
	"shelfUnit":        true,
	"light":            true,
}

func decode(b []byte) (*Home, error) {
	h := &Home{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var (
		room   *Room
		groups []string // level of each open furnitureGroup
		sawTop bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing Home.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			a := attrs(t.Attr)
			if !sawTop {
				if t.Name.Local != "home" {
					return nil, fmt.Errorf("Home.xml starts with <%s>, not <home>", t.Name.Local)
				}
				sawTop = true
				continue
			}
			// A group's children often carry no level of their own; they sit
			// on the group's.
			level := a.str("level")
			if level == "" && len(groups) > 0 {
				level = groups[len(groups)-1]
			}
			switch name := t.Name.Local; {
			case name == "level":
				h.Levels = append(h.Levels, Level{
					ID: a.str("id"), Name: a.str("name"),
					Elevation: a.num("elevation"), ElevationIndex: int(a.num("elevationIndex")),
				})
			case name == "wall":
				h.Walls = append(h.Walls, Wall{
					Level:  level,
					XStart: a.num("xStart"), YStart: a.num("yStart"),
					XEnd: a.num("xEnd"), YEnd: a.num("yEnd"),
					Thickness: a.num("thickness"),
				})
			case name == "room":
				room = &Room{
					Level: level, Name: a.str("name"),
					NameXOffset: a.num("nameXOffset"), NameYOffset: a.num("nameYOffset"),
				}
			case name == "point" && room != nil:
				room.Points = append(room.Points, Point{a.num("x"), a.num("y")})
			case name == "furnitureGroup":
				groups = append(groups, level)
			case furnitureTags[name]:
				if a.str("visible") == "false" {
					continue
				}
				p := Piece{
					Level: level, Name: a.str("name"),
					X: a.num("x"), Y: a.num("y"),
					Width: a.num("width"), Depth: a.num("depth"),
					Angle: a.num("angle"), Elevation: a.num("elevation"),
					DoorOrWindow:  name == "doorOrWindow",
					WallThickness: 1,
				}
				// SweetHome3D omits both when they are the defaults: the whole
				// depth is wall, none of it in front.
				if a.has("wallThickness") {
					p.WallThickness = a.num("wallThickness")
				}
				p.WallDistance = a.num("wallDistance")
				h.Furniture = append(h.Furniture, p)
			}
			if a.err != nil {
				return nil, fmt.Errorf("<%s>: %w", t.Name.Local, a.err)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "room":
				if room != nil {
					h.Rooms = append(h.Rooms, *room)
					room = nil
				}
			case "furnitureGroup":
				if len(groups) > 0 {
					groups = groups[:len(groups)-1]
				}
			}
		}
	}
	if !sawTop {
		return nil, errors.New("Home.xml is empty")
	}
	return h, nil
}

// FirstLevel returns only what sits on the lowest level, the one SweetHome3D
// opens on. A home without levels is returned whole.
func (h *Home) FirstLevel() *Home {
	if len(h.Levels) == 0 {
		return h
	}
	levels := append([]Level(nil), h.Levels...)
	sort.SliceStable(levels, func(i, j int) bool {
		if levels[i].Elevation != levels[j].Elevation {
			return levels[i].Elevation < levels[j].Elevation
		}
		return levels[i].ElevationIndex < levels[j].ElevationIndex
	})
	id := levels[0].ID
	out := &Home{Levels: []Level{levels[0]}}
	for _, w := range h.Walls {
		if w.Level == id {
			out.Walls = append(out.Walls, w)
		}
	}
	for _, r := range h.Rooms {
		if r.Level == id {
			out.Rooms = append(out.Rooms, r)
		}
	}
	for _, p := range h.Furniture {
		if p.Level == id {
			out.Furniture = append(out.Furniture, p)
		}
	}
	return out
}

type attrMap struct {
	m   map[string]string
	err error
}

func attrs(list []xml.Attr) *attrMap {
	a := &attrMap{m: make(map[string]string, len(list))}
	for _, at := range list {
		a.m[at.Name.Local] = at.Value
	}
	return a
}

func (a *attrMap) str(k string) string { return a.m[k] }

func (a *attrMap) has(k string) bool { _, ok := a.m[k]; return ok }

// num returns 0 for a missing attribute, which is SweetHome3D's own default for
// every number it omits. A malformed or non-finite one is an error rather than a
// wall drawn to infinity.
func (a *attrMap) num(k string) float64 {
	s, ok := a.m[k]
	if !ok {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err == nil && (math.IsNaN(v) || math.IsInf(v, 0)) {
		err = errors.New("not a finite number")
	}
	if err != nil {
		if a.err == nil {
			a.err = fmt.Errorf("attribute %s=%q: %w", k, s, err)
		}
		return 0
	}
	return v
}

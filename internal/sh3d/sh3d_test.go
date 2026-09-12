package sh3d

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func archive(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func read(t *testing.T, homeXML string) (*Home, error) {
	t.Helper()
	r := archive(t, map[string]string{"Home": "serialised java", "Home.xml": homeXML, "0": "model"})
	return Read(r, r.Size())
}

const flat = `<?xml version='1.0'?>
<home version='7400' name='Test.sh3d'>
  <property name='x' value='y'/>
  <wall id='w1' xStart='0.0' yStart='10.0' xEnd='1.3896343E-13' yEnd='285.0' thickness='7.62'/>
  <room name='Kitchen' nameXOffset='5' nameYOffset='-10'>
    <point x='0' y='0'/><point x='100' y='0'/><point x='100' y='50'/>
  </room>
  <polyline><point x='1' y='1'/></polyline>
  <doorOrWindow name='Door' x='672.5' y='410.5' width='69' depth='14.73' height='197'
      wallThickness='0.517' wallDistance='0.069' angle='1.5707964'>
    <sash xAxis='0.05' yAxis='0.5' width='0.9' startAngle='0' endAngle='-1.57'/>
  </doorOrWindow>
  <doorOrWindow name='Window' x='770' y='510' elevation='87' width='110' depth='7.62'/>
  <furnitureGroup name='Desk set' level='l1' x='10' y='10' width='5' depth='5'>
    <pieceOfFurniture name='Chair' x='117.5' y='135.6' width='48.6' depth='49.9'/>
  </furnitureGroup>
  <shelfUnit name='Shelf' x='264' y='412' width='100' depth='40'><shelf elevation='0.1'/></shelfUnit>
  <light name='Lamp' x='1' y='2' width='3' depth='4' power='0.5'/>
  <pieceOfFurniture name='Hidden' x='1' y='1' width='1' depth='1' visible='false'/>
</home>`

func TestReadGeometry(t *testing.T) {
	h, err := read(t, flat)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Walls) != 1 || h.Walls[0].YEnd != 285 || h.Walls[0].Thickness != 7.62 {
		t.Errorf("walls = %+v", h.Walls)
	}
	if len(h.Rooms) != 1 {
		t.Fatalf("rooms = %+v, want one: a polyline's points are not a room's", h.Rooms)
	}
	if r := h.Rooms[0]; r.Name != "Kitchen" || len(r.Points) != 3 || r.NameXOffset != 5 || r.NameYOffset != -10 {
		t.Errorf("room = %+v", r)
	}

	byName := map[string]Piece{}
	for _, p := range h.Furniture {
		byName[p.Name] = p
	}
	if len(h.Furniture) != 5 {
		t.Errorf("furniture = %d pieces, want door, window, chair, shelf and lamp", len(h.Furniture))
	}
	if _, ok := byName["Hidden"]; ok {
		t.Error("an invisible piece was read")
	}
	if _, ok := byName["Desk set"]; ok {
		t.Error("a furniture group was read as a piece rather than flattened")
	}
	if c := byName["Chair"]; c.Level != "l1" || c.X != 117.5 {
		t.Errorf("chair = %+v, want the group's level and its own coordinates", c)
	}
	if d := byName["Door"]; !d.DoorOrWindow || d.WallThickness != 0.517 || d.WallDistance != 0.069 || d.Angle != 1.5707964 {
		t.Errorf("door = %+v", d)
	}
	if w := byName["Window"]; !w.DoorOrWindow || w.WallThickness != 1 || w.WallDistance != 0 || w.Elevation != 87 {
		t.Errorf("window = %+v, want the defaults SweetHome3D leaves out", w)
	}
	if s := byName["Shelf"]; s.DoorOrWindow || s.Width != 100 {
		t.Errorf("shelf = %+v", s)
	}
}

func TestFirstLevel(t *testing.T) {
	h, err := read(t, `<home>
  <level id='up' name='Upstairs' elevation='250' elevationIndex='0'/>
  <level id='b' name='Ground B' elevation='0' elevationIndex='1'/>
  <level id='a' name='Ground A' elevation='0' elevationIndex='0'/>
  <wall level='up' xStart='0' yStart='0' xEnd='1' yEnd='0' thickness='1'/>
  <wall level='a' xStart='0' yStart='0' xEnd='2' yEnd='0' thickness='1'/>
  <room level='b' name='B'><point x='0' y='0'/></room>
  <pieceOfFurniture level='a' name='Sofa' x='1' y='1' width='1' depth='1'/>
</home>`)
	if err != nil {
		t.Fatal(err)
	}
	f := h.FirstLevel()
	if len(f.Levels) != 1 || f.Levels[0].ID != "a" {
		t.Fatalf("first level = %+v, want the lowest elevation, then the lowest index", f.Levels)
	}
	if len(f.Walls) != 1 || f.Walls[0].XEnd != 2 || len(f.Rooms) != 0 || len(f.Furniture) != 1 {
		t.Errorf("first level holds %d walls, %d rooms, %d pieces", len(f.Walls), len(f.Rooms), len(f.Furniture))
	}

	flatHome, err := read(t, flat)
	if err != nil {
		t.Fatal(err)
	}
	if flatHome.FirstLevel() != flatHome {
		t.Error("a home without levels should come back whole")
	}
}

func TestReadRejects(t *testing.T) {
	cases := []struct {
		name string
		file *bytes.Reader
		want string
	}{
		{"not a zip", bytes.NewReader([]byte(`{"width": 800}`)), "not a ZIP"},
		{"old file", archive(t, map[string]string{"Home": "serialised java"}), "5.3"},
		{"wrong root", archive(t, map[string]string{"Home.xml": "<plan/>"}), "not <home>"},
		{"empty", archive(t, map[string]string{"Home.xml": ""}), "empty"},
		{"malformed number", archive(t, map[string]string{"Home.xml": "<home><wall xStart='abc'/></home>"}), "xStart"},
		{"not finite", archive(t, map[string]string{"Home.xml": "<home><wall xStart='NaN'/></home>"}), "finite"},
		{"broken xml", archive(t, map[string]string{"Home.xml": "<home><wall"}), "parsing"},
	}
	for _, c := range cases {
		_, err := Read(c.file, c.file.Size())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

// Sixteen megabytes of spaces deflate to a few kilobytes, which is exactly the
// shape of a ZIP bomb.
func TestReadCapsHomeXML(t *testing.T) {
	big := "<home>" + strings.Repeat(" ", MaxXML) + "</home>"
	r := archive(t, map[string]string{"Home.xml": big})
	if r.Size() > 1<<20 {
		t.Fatalf("test archive is %d bytes, expected it to compress", r.Size())
	}
	if _, err := Read(r, r.Size()); err == nil || !strings.Contains(err.Error(), "allowed") {
		t.Errorf("err = %v, want the size cap", err)
	}
}

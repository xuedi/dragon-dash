package fritzbox

import "testing"

const deviceList = `<devicelist version="1">
  <device identifier="11630 0343807" productname="FRITZ!DECT 200">
    <present>1</present><name>Kitchen</name>
    <switch><state>0</state></switch>
    <powermeter><voltage>231040</voltage><power>0</power><energy>12345</energy></powermeter>
  </device>
  <device identifier="11657 0240192" productname="FRITZ!Smart Energy 250">
    <present>1</present><name>House</name>
    <powermeter><voltage>0</voltage><power>0</power><energy>3015200</energy></powermeter>
  </device>
</devicelist>`

// A plug that is switched off still sees mains, so 0 W at 231 V is a reading.
// A meter reporting 0 V has none, and its 0 W must not become one.
func TestZeroVoltMeterHasNoPowerReading(t *testing.T) {
	devices, err := parseDevices([]byte(deviceList))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}

	plug := devices[0]
	if plug.AIN != "116300343807" {
		t.Errorf("AIN = %q, want the space collapsed", plug.AIN)
	}
	if plug.PowerW == nil || *plug.PowerW != 0 {
		t.Errorf("idle plug power = %v, want 0 W", plug.PowerW)
	}
	if plug.VoltageV == nil || *plug.VoltageV != 231.04 {
		t.Errorf("plug voltage = %v, want 231.04 V", plug.VoltageV)
	}

	meter := devices[1]
	if meter.PowerW != nil || meter.VoltageV != nil {
		t.Errorf("0 V meter reports power %v and voltage %v, want neither", meter.PowerW, meter.VoltageV)
	}
	if meter.EnergyKWh == nil || *meter.EnergyKWh != 3015.2 {
		t.Errorf("meter energy = %v, want 3015.2 kWh", meter.EnergyKWh)
	}
}

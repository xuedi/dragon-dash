package fritzbox

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
)

// Device is one smart home device in the units you would actually display,
// not the units AVM transmits.
type Device struct {
	AIN     string
	Name    string
	Product string
	Present bool

	// Set only when the device has the corresponding capability.
	PowerW    *float64 // current draw, watts
	EnergyKWh *float64 // lifetime total, kilowatt hours
	VoltageV  *float64
	TempC     *float64
	HumidityP *float64

	SwitchOn *bool

	TargetC    *float64 // thermostat setpoint
	BatteryPct *float64
	BatteryLow *bool
}

type rawList struct {
	XMLName xml.Name    `xml:"devicelist"`
	Devices []rawDevice `xml:"device"`
}

type rawDevice struct {
	AIN     string `xml:"identifier,attr"`
	Product string `xml:"productname,attr"`
	Name    string `xml:"name"`
	Present int    `xml:"present"`

	Switch *struct {
		State string `xml:"state"`
	} `xml:"switch"`

	PowerMeter *struct {
		Voltage *int64 `xml:"voltage"` // mV
		Power   *int64 `xml:"power"`   // mW
		Energy  *int64 `xml:"energy"`  // Wh
	} `xml:"powermeter"`

	Temperature *struct {
		Celsius *int64 `xml:"celsius"` // 0.1 C
	} `xml:"temperature"`

	Humidity *struct {
		RelHumidity *int64 `xml:"rel_humidity"` // %
	} `xml:"humidity"`

	HKR *struct {
		Tsoll      *int64 `xml:"tsoll"`   // half degrees
		Battery    *int64 `xml:"battery"` // %
		BatteryLow *int64 `xml:"batterylow"`
	} `xml:"hkr"`
}

// Devices fetches every smart home device in one call.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	body, err := c.aha(ctx, "getdevicelistinfos")
	if err != nil {
		return nil, err
	}
	return parseDevices(body)
}

func parseDevices(body []byte) ([]Device, error) {
	var list rawList
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing device list: %w", err)
	}

	out := make([]Device, 0, len(list.Devices))
	for _, r := range list.Devices {
		d := Device{
			// AVM writes the AIN with a space ("08761 0000434"); collapsing it
			// keeps it usable as a Prometheus label and a URL fragment.
			AIN:     strings.ReplaceAll(strings.TrimSpace(r.AIN), " ", ""),
			Name:    strings.TrimSpace(r.Name),
			Product: strings.TrimSpace(r.Product),
			Present: r.Present == 1,
		}
		if r.Switch != nil && r.Switch.State != "" {
			on := r.Switch.State == "1"
			d.SwitchOn = &on
		}
		if p := r.PowerMeter; p != nil {
			d.EnergyKWh = scale(p.Energy, 1000) // Wh  -> kWh
			// Anything on mains sees its voltage, even switched off, so 0 V is
			// no reading at all. An Energy 250 on a house meter read without the
			// meter's PIN gets only the total and reports 0 V and 0 W; passing
			// those on would chart the whole flat at a flat 0 W.
			if p.Voltage == nil || *p.Voltage != 0 {
				d.PowerW = scale(p.Power, 1000)     // mW  -> W
				d.VoltageV = scale(p.Voltage, 1000) // mV  -> V
			}
		}
		if t := r.Temperature; t != nil {
			d.TempC = scale(t.Celsius, 10) // 0.1 C -> C
		}
		if h := r.Humidity; h != nil {
			d.HumidityP = scale(h.RelHumidity, 1)
		}
		if h := r.HKR; h != nil {
			// Thermostat setpoints are in half degrees. 253 and 254 are the
			// sentinel values for "off" and "always on", not temperatures.
			if h.Tsoll != nil && *h.Tsoll < 253 {
				d.TargetC = scale(h.Tsoll, 2)
			}
			d.BatteryPct = scale(h.Battery, 1)
			if h.BatteryLow != nil {
				low := *h.BatteryLow == 1
				d.BatteryLow = &low
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func scale(v *int64, div float64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v) / div
	return &f
}

package fritzhome

import (
	"context"
	"time"

	"armdash/internal/chart"
)

// charts read what Collect publishes back out of Prometheus.
//
// Every query aggregates by ain. The name label follows the device's name in
// the FRITZ!Box, so a rename would fork a line in two; lines are named from
// the live device list instead.
var charts = []chart.Chart{
	{Slug: "temperatures", Title: "Temperatures", Unit: "°C",
		Query: `max by (ain) (fritz_temperature_celsius)`},
	{Slug: "power", Title: "Power", Unit: "W", FromZero: true, Series: []chart.Series{
		{Query: powerByDevice},
		{Name: "Total", Query: `sum(` + powerByDevice + `)`},
	}},
	{Slug: "energy", Title: "Energy", Unit: "kWh", FromZero: true,
		Bucket: chart.HourOrDay, Window: 24 * time.Hour,
		Query: `max by (ain) (increase(fritz_energy_kwh_total[$bucket]))`},
	{Slug: "humidity", Title: "Humidity", Unit: "%", FromZero: true,
		Query: `max by (ain) (fritz_humidity_percent)`},
}

// powerByDevice leaves out a meter reporting 0 V, which has no power reading.
// The poll drops such readings itself now; this keeps the ones recorded before
// that off the chart too.
const powerByDevice = `max by (ain) (fritz_power_watts) unless on (ain) (max by (ain) (fritz_voltage_volts) == 0)`

// lineNames names lines by each device's current name. A device the box no
// longer reports, or every device while the box cannot be reached, keeps its
// AIN.
func (f *FritzHome) lineNames(ctx context.Context) chart.Namer {
	names := map[string]string{}
	if f.configured() {
		// Short: the names are a nicety, the chart is the point.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		devices, _ := f.devices(ctx)
		for _, d := range devices {
			names[d.AIN] = d.Name
		}
	}
	return func(labels map[string]string) string {
		if n := names[labels["ain"]]; n != "" {
			return n
		}
		return labels["ain"]
	}
}

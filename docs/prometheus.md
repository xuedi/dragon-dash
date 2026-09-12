# Prometheus

Server metrics come from Prometheus. armdash reads `/proc` for nothing and stores no samples of
its own.

Smart home data is the exception in one direction only: armdash polls the FRITZ!Box itself and
publishes the result at `/metrics`, which Prometheus scrapes. The live page shows the poll directly;
the charts read the same numbers back out of Prometheus.

## Why

- **History for free.** A gauge read from `/proc` can only ever show *now*. Read the same value
  from Prometheus and the identical query yields a year of it.
- **It works remotely.** Development happens on a desktop against a server's Prometheus. Nothing
  in the code assumes the metrics describe the machine it runs on.
- **The dashboard stays stateless.** No retention policy, no compaction, no backup of its own.

The cost is a hard dependency: with no Prometheus URL configured, the dashboards have nothing to
show. Pages say exactly that rather than rendering zeroes, which would be a lie.

## The client

`internal/promql` is a small wrapper over the HTTP API, `Query`, `QueryOne` and `QueryRange`.

Two things it handles that trip people up:

- **Values arrive as JSON strings**, not numbers, including `"NaN"`, `"+Inf"` and `"-Inf"`.
  Parsing goes through `strconv.ParseFloat`, and unparseable points are dropped rather than
  becoming zero.
- **The base URL is a function, not a string.** It is editable in Settings at runtime, and a cached
  copy would mean a restart after every change.

An empty result is **not** an error. A metric can legitimately be absent, either because there is
no such device or because the exporter has not started yet. `QueryOne` returns `ok=false` and the
UI shows a dash, not a zero.

## Query conventions

Range queries target **roughly 800 points regardless of window**, so a year costs no more to draw
than an hour:

```go
step := time.Duration(window/800) * time.Second
```

A bar chart is the exception: it steps by its bucket, an hour or a day, on bucket boundaries, and
its query takes the bucket as `$bucket`, as in `increase(counter[$bucket])`. Sampling an increase
once per bucket over exactly one bucket counts every moment once.

A chart is one query or several. Where a chart draws several lines they are aligned **by timestamp,
not by index**: the queries share a start, end and step, so Prometheus returns the same grid, but a
series with no data for part of the window is missing those points entirely. A sample with no value
is sent to the browser as `null` and drawn as a gap, never as zero.

Server metrics assume `prometheus-node-exporter`. Smart home metrics are armdash's own `fritz_*`
series, published at `/metrics`; see [fritzbox-metrics.md](fritzbox-metrics.md) for why it is not a
separate exporter.

## Smart home

FritzHome charts temperatures, power, energy and humidity from the series it publishes itself.
Three rules keep those charts honest:

- **Aggregate by `ain`.** Every series carries the device's `name` label, and that follows a rename
  in the FRITZ!Box, so a raw query would fork a device's line in two at the moment it was renamed.
  Every query is `max by (ain) (...)`, the same reason the thermal zones are aggregated by `type`.
- **Names come from the poll, not from Prometheus.** A line is labelled with the device's current
  name from the cached device list, so a renamed device shows its new name across its whole
  history. A device the box no longer reports, or every device while the box cannot be reached,
  is labelled with its AIN.
- **A meter reporting 0 V has no power reading.** A FRITZ!Smart Energy 250 on a house meter,
  read without the meter's PIN, gets only the energy total and reports 0 W at 0 V. Anything
  plugged into mains sees its voltage even when switched off or idle, so 0 V means no reading, and
  the poll publishes neither power nor voltage for it. The house then appears in Energy only. The
  Power query also leaves out any device whose voltage was 0, which keeps readings recorded before
  that rule off the chart.

## Temperatures

Three different metrics carry temperatures, and picking the wrong one is a silent mistake rather
than an error.

| Metric | Covers | Names things as |
|---|---|---|
| `node_thermal_zone_temp` | every kernel thermal zone | `type="cpuss0-thermal"`, the kernel's own name |
| `node_hwmon_temp_celsius` | the same zones **and** any hwmon chip, such as an NVMe drive | `chip="thermal_thermal_zone31"`, an opaque index |
| `smartctl_device_temperature` | drives readable only over SMART | `device="/dev/sda"` |

node_exporter publishes the board's thermal zones twice, once through each of the first two
collectors. Only `node_thermal_zone_temp` names them, so that is what the per-component lines on
the Thermals chart and the "hottest zone" figure both read. `node_hwmon_temp_celsius` is used for
exactly one thing: the internal NVMe drive, which is a hwmon chip and not a thermal zone. Its
`temp1` sensor is the drive's own Composite reading; `temp2` and `temp3` are the two sensors behind
it.

The consequence worth remembering is that `max(node_hwmon_temp_celsius)` is **not** a board figure.
It includes the internal drive, so it can report the drive while appearing to report the board.

**Aggregate every temperature query.** The kernel renumbers thermal zones across boots, so
`node_thermal_zone_temp` carries a `zone` label that forks a fresh series on every reboot; the same
sensor has been zone 29, 30 and 31 within one afternoon. Reading the raw metric draws one broken
line per numbering era, all under the same name. `max by (type) (...)` stitches them back into the
one sensor they are.

### The external USB SSD

One drive is a SCSI disk behind a UAS bridge. It has no hwmon device and no thermal zone, so no
node_exporter collector can ever see it, and its temperature is only reachable by tunnelling NVMe
admin commands through the bridge:

```
smartctl -d sntasmedia -A /dev/sda
```

**armdash does not run that itself.** It runs as a hardened non-root service and the command
needs root, so reading SMART here would mean granting the dashboard a privilege it lives without
today. The FRITZ!Box is not a precedent: that is an HTTP poll of a network device, not a privileged
local syscall.

The reading arrives the same way as everything else, through Prometheus. A node_exporter **textfile
collector** on the server, run by a timer, writes:

```
smartctl_device_temperature{device="/dev/sda",temperature_type="current"} 31
```

That name and those labels are `smartctl_exporter`'s, deliberately, rather than a private
invention: if the drive count ever grows enough to justify the real exporter, swapping the script
for it changes no query here. The collector publishes exactly one drive, so the dashboard reads
`max(smartctl_device_temperature{temperature_type="current"})`; an exporter covering both drives
would need a `device` filter instead.

A textfile collector must **not** write into `node_hwmon_temp_celsius` or `node_thermal_zone_temp`.
Those namespaces belong to node_exporter, and a collision there would quietly change what the
existing queries mean.

Until that collector exists the metric is simply absent, which is a supported state everywhere: the
overview card shows a dash and the chart omits the line.

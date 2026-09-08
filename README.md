# dragon-dash

![version](https://img.shields.io/badge/version-0.4.0-blue)
![licence](https://img.shields.io/badge/licence-EUPL--1.2-brightgreen)

A single-binary web dashboard for a home server. One tab per *system*: server
metrics, FRITZ!Box smart home, and whatever comes next. Everything is read from
Prometheus, so every number on screen has history behind it.

Built for [dragon](https://docs.radxa.com/en/dragon/q6a), a Radxa Dragon Q6A
running Arch Linux ARM, but nothing in it is specific to that board.

> **Status: early.** The shell, the settings system and two systems exist and
> run. Development happens on a desktop; deploying to the server comes later.

## Why it exists

Grafana is the obvious answer and it is a good one, but it is a large dependency
to run permanently on a 12 W box, it is **not packaged for aarch64** at all (neither in Arch Linux ARM's repos nor the
AUR), and most of it goes unused when
all you want is a handful of charts and a floor plan of your flat.

dragon-dash is the small version of that: one static binary, no database, no
node toolchain, no runtime dependencies.

## Stack

|                                            |                        |                                                        |
|--------------------------------------------|------------------------|--------------------------------------------------------|
| Go, standard library only                  | no external Go modules | the binary is the deployment                           |
| `html/template` + [htmx](https://htmx.org) | server-rendered        | no build step, no npm                                  |
| [Bulma](https://bulma.io) 1.0              | CSS only               | no JS framework to age                                 |
| [uPlot](https://github.com/leeoniya/uPlot) | charts                 | a year at 60s is ~500k points; uPlot is built for that |

Bulma, htmx and uPlot are **committed as files** and embedded with `go:embed`.
There is no `package.json` and never will be.

## Architecture

The shell knows nothing about any individual system. It asks each one what it is
called, what belongs in its sidebar, and what settings it needs, all as data,
and renders the result:

```go
type System interface {
ID() string    // "dragon"
Title() string // navbar label
Nav() []NavItem // left sidebar, as data
ConfigSchema() []ConfigField // settings page generates itself from this
Render(slug string, r *http.Request) (template.HTML, error)
Register(mux *http.ServeMux, prefix string, deps Deps)
}
```

Systems are **compiled in** and register themselves in `init()`. Whether one
appears is a config decision, not a build one, the settings page has a switch
per system.

There is deliberately no runtime plugin loading. Go's `plugin` package cannot
cross-compile and demands an identical toolchain and identical dependency
versions, which would cost exactly the single-binary property that makes this
thing pleasant to deploy. If out-of-process plugins ever earn their keep, the
interface above is the seam they would go through.

### Adding a system

1. Create `internal/systems/<name>/`, implement `system.System`, call
   `system.Register` from `init()`.
2. Blank-import it in `cmd/dragon-dash/main.go`.

There is no third step. Navigation, routing and the settings form follow.

## Running it

```bash
just run          # http://127.0.0.1:9494
just run-lan      # reachable from other machines
just check        # gofmt, vet, tests
```

Then open **Settings** and set the Prometheus URL. Until you do, every page
politely says so rather than showing zeroes.

For real data while developing, run Prometheus and node_exporter on the desktop:

```bash
just dev-up       # prometheus on :9090, node_exporter on :9100
```

and point Settings at `http://127.0.0.1:9090`.

### Configuration

One JSON file, `config.json` by default (`-config` to move it), written `0600`
because it can hold credentials. Deliberately not SQLite: this is configuration,
not data. Real data belongs in Prometheus.

## The systems

### Dragon

Server metrics from `prometheus-node-exporter`: CPU, memory, filesystem usage,
hwmon temperatures, load and uptime. An overview of current values, plus a chart
per metric with ranges from one hour to one year.

### FritzHome

FRITZ!Box smart home data: smart plug power and energy, room temperatures,
humidity, thermostat setpoints and battery levels.

dragon-dash talks to the box itself over AVM's documented interfaces
(`login_sid.lua` with the PBKDF2 challenge, then the AHA-HTTP-Interface), so
there is no separate exporter to run and the credentials live in one place. The
page shows the live reading; the same reading is published at `/metrics` for
Prometheus to keep as history.

Includes a **floor plan**: the outer wall, rooms, interior walls and doors as
SVG geometry, with devices placed by AIN and showing their live reading. It is
a JSON file (`DD_SYSTEM_FRITZHOME_FLOORPLAN_FILE`), so redrawing the flat does
not mean recompiling.

Background on why Prometheus rather than InfluxDB, storage sizing, and the
exporters that were evaluated and rejected: [`docs/fritzbox-metrics.md`](docs/fritzbox-metrics.md).

## Deployment

Not deployed yet. When it is, it will be the same binary cross-compiled:

```bash
just build-arm    # bin/dragon-dash-arm64, static, ~10 MB
```

`deploy/` holds a Compose stack (dragon-dash + Prometheus + node_exporter) that
publishes `80:8080`. Port 80 is a port mapping, so the process never needs root
or `CAP_NET_BIND_SERVICE`.

## Security

**There is no authentication.** Anyone who can reach the port can read Settings.
That is a deliberate choice for a LAN tool, but it means dragon-dash must not be
port-forwarded or exposed to the internet.

## Licence

[EUPL-1.2](LICENSE).

## Contributing

Run `just check` before anything else: gofmt, vet, tests. The version constant in
`internal/version/version.go` and the badge above move together in the same commit.

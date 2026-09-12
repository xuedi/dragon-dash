# armdash

![version](https://img.shields.io/badge/version-0.12.1-blue)
![licence](https://img.shields.io/badge/licence-EUPL--1.2-brightgreen)

A single-binary web dashboard for a home server. One tab per *system*: server
metrics, FRITZ!Box smart home, and whatever comes next. Everything is read from
Prometheus, so every number on screen has history behind it.

Built for a small always-on ARM home server running Arch Linux ARM, but
nothing in it is specific to any particular board.

<img src="docs/images/floorplan.png" alt="FritzHome floor plan" width="700">

> **SweetHome3D floor plans can be uploaded straight from the page.** Drop in
> the `.sh3d` file of your flat and drag the FRITZ!Box sensors to where they
> are.

## Why it exists

Grafana is the obvious answer and it is a good one, but it is a large dependency
to run permanently on a 12 W box, it is **not packaged for aarch64** at all
(neither in Arch Linux ARM's repos nor the AUR), and most of it goes unused when
all you want is a handful of charts and a floor plan of your flat.

armdash is the small version of that: one static binary, no database, no
node toolchain, no runtime dependencies. Go with the standard library only,
server-rendered `html/template` with [htmx](https://htmx.org),
[Bulma](https://bulma.io) for the CSS and [uPlot](https://github.com/leeoniya/uPlot)
for the charts, all three committed as files and embedded with `go:embed`.
There is no `package.json` and never will be.

## What it shows

### Host

Server metrics from `prometheus-node-exporter`: CPU, memory, filesystem usage,
temperatures, load and uptime. An overview of current values, plus a chart per
metric with ranges from one hour to one year. The thermals chart names its
lines, so a warm board says which part is warm.

### FritzHome

FRITZ!Box smart home data: smart plug power and energy, room temperatures,
humidity, thermostat setpoints and battery levels.

armdash talks to the box itself over AVM's documented interfaces, so there
is no separate exporter to run and the credentials live in one place. The page
shows the live reading; the same reading is published at `/metrics` for
Prometheus to keep as history.

The floor plan above shows every device at its spot with its live reading.

- **SweetHome3D import.** Upload the `.sh3d` file itself, nothing exported:
  walls, rooms and their names, doors, windows and furniture outlines are read
  from it and drawn to scale. An SVG or a picture of the flat works too, and so
  does a hand-traced JSON file.
- **Drag and drop placement.** Press Edit and drag each sensor to where it
  sits, or back off the plan. Positions are saved on the server, so redrawing
  the flat never means recompiling or retyping coordinates.

Details in [`docs/floorplan.md`](docs/floorplan.md).

### Links

Extra navbar entries for the other things on the server, like the Wiki in the
screenshot, a Grafana or the FRITZ!Box itself. Each shows below the navbar in a
frame, through a built-in reverse proxy, or opens in a new tab, from a few
`AD_LINK_*` lines of configuration. Details in [`docs/links.md`](docs/links.md).

## Installing

Grab a package or a tarball from [releases](https://github.com/xuedi/armdash/releases).
Every version that lands on `main` is built and released automatically, each
with static amd64 and arm64 binaries plus `.deb`, `.rpm` and Arch packages, all
built from the same commit.

```bash
sudo pacman -U armdash_*_linux_arm64.pkg.tar.zst   # or dpkg -i / rpm -i
armdash passwd                                     # prints the login lines
sudoedit /etc/armdash/armdash.env                  # address, Prometheus, FRITZ!Box, login
sudo systemctl enable --now armdash
```

`just install` does the same from a checkout of this repository.

The package installs a hardened systemd unit that runs as a dedicated
unprivileged user with the filesystem read-only except for one directory,
`/var/lib/armdash`, which holds an uploaded floor plan and the device
positions. Configuration is read-only and the history lives in Prometheus.
`CAP_NET_BIND_SERVICE` is granted so ports 80 and 443 work without root.

armdash keeps no history itself, so a full deployment is three services:
node_exporter and armdash's own `/metrics` are scraped by Prometheus, and
armdash queries Prometheus back to draw the pages. Both exporters are
packaged for aarch64, so none of it needs containers. `deploy/` also holds a
Compose stack for hosts where containers are preferred. Details in
[`docs/deployment.md`](docs/deployment.md).

## Configuring it

Env files and the environment, nothing else: `.env.dist` for committed defaults,
`.env.local` for credentials, real environment variables winning over both. The
packaged unit reads `/etc/armdash/armdash.env` instead.

```ini
AD_CORE_ADDR=127.0.0.1:9494
AD_CORE_PROMETHEUS_URL=http://127.0.0.1:9090
AD_SYSTEM_FRITZHOME_URL=http://fritz.box
```

Configuration is **read-only at runtime**, which is the point. The Settings page
shows what is set and where each value came from, but nothing writes it back,
not even for someone logged in: the password changes by editing the file. Full
key list in [`docs/configuration.md`](docs/configuration.md).

Setting `AD_CORE_TLS_CERT` and `AD_CORE_TLS_KEY` turns on HTTPS on
`AD_CORE_TLS_ADDR`. The plain port then redirects there, except `/metrics`,
which Prometheus keeps scraping over HTTP. Details in
[`docs/deployment.md`](docs/deployment.md#https).

`AD_LINKS` lists the extra navbar entries, each with its own `AD_LINK_<ID>_*`
lines, see [`docs/links.md`](docs/links.md).

## Building and running from source

```bash
just run          # http://127.0.0.1:9494
just run-lan      # reachable from other machines
just build-arm    # static arm64 binary, no cgo, target needs no toolchain
just check        # gofmt, vet, tests
just install      # install this checkout as a service, the way the packages do
```

Until `AD_CORE_PROMETHEUS_URL` points somewhere real, every page politely says
so rather than showing zeroes. For real data while developing, run Prometheus
and node_exporter on the desktop with `just dev-up` (prometheus on `:9090`,
node_exporter on `:9100`).

## Security

Changing anything needs the one owner login: uploading a floor plan, placing
devices and opening Settings. The dashboards stay open to anyone who can reach
the port, like a display on the wall, and `/metrics` stays open for Prometheus.
The password is kept as a PBKDF2 hash, sessions expire on the server after
seven days, and failed logins are limited per address. Without a login
configured, nothing can be changed at all. Details in
[`docs/authentication.md`](docs/authentication.md).

It is still a LAN tool: do not port-forward it or expose it to the internet.

## Licence

[EUPL-1.2](LICENSE).

## Contributing

Run `just check` first: gofmt, vet, tests. How the thing is put together, the
system interface, the config layers, the Prometheus client and the frontend, is
written up in [`docs/`](docs/README.md).

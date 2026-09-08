# dragon-dash, long-term FRITZ!Box smart home metrics

**Status: stack written, nothing deployed.** Researched and scaffolded 2026-09-08.
Docker is **not yet installed on dragon**.

## The goal

The FRITZ!Box collects smart home sensor data, smart plug power draw, energy totals, temperatures
- but keeps it only briefly. The aim is **indefinite retention** of those metrics plus dashboards
over the history.

Explicitly *not* Home Assistant: it duplicates automation the FRITZ!Box already does natively, and
the FRITZ!Box UI is the preferred place for automations. This project is storage + graphs only.

## Why the old approach broke, and what fixes it

The previous attempt was InfluxDB plus a hand-written Python script, and **the script broke on
every FRITZ!OS update**. That is the important failure to design out.

The cause is almost always talking to the *web UI*, session tokens, HTML scraping, undocumented
endpoints, all of which AVM changes freely. The two APIs that stay stable across firmware releases
are:

- **TR-064** (SOAP, port 49000), router, WAN, DSL, WLAN statistics.
- **AHA-HTTP-Interface** (`/webservices/homeautoswitch.lua`), the DECT smart home data. AVM
  publishes a [spec PDF](https://avm.de/fileadmin/user_upload/Global/Service/Schnittstellen/AHA-HTTP-Interface.pdf)
  for it.

**Use a maintained exporter that speaks those APIs**, so somebody else absorbs firmware churn.

## Proposed stack

```
FRITZ!Box ──TR-064 + AHA──▶ fritz_exporter ──scrape──▶ Prometheus ──HTTP API──▶ dashboard
```

### Collector: `pdreker/fritz_exporter`

<https://github.com/pdreker/fritz_exporter>, Python, Prometheus exporter, built on `fritzconnection`.

Verified 2026-09-08: active (last push 2026-08-24, **not** archived, 206 stars), and
`fritzexporter/fritz_aha.py` confirms it implements the AHA interface, emitting exactly what this
project needs:

| Parser | Metrics |
|---|---|
| `_parse_powermeter` | `power`, `energy` |
| `_parse_temperature` | `celsius`, `offset` |
| `_parse_switch` | `state`, `mode`, `lock` |
| HKR / battery | `battery_level`, `battery_low` |

That source also carries a comment, *"On some firmware versions battery data is only reported
nested"*, which is direct evidence the maintainer tracks firmware differences. That is precisely
the work that used to land on the hand-written script.

Router-side TR-064 metrics come along for free.

> **Checked and rejected:** Telegraf's official `fritzbox` input plugin (core since v1.35.0) covers
> only `device`, `wan`, `ppp`, `dsl`, `fiber`, `wlan`, `hosts`, **no smart home data**. The
> third-party `hdecarne-github/fritzbox-telegraf-plugin` was archived 2025-03-22 and was TR-064 only.
> `jayme-github/fritzbox_smarthome_exporter` does the right thing via AHA but was **archived
> 2026-02-18**. Do not spend time re-evaluating these.

### Storage: Prometheus, not InfluxDB

`extra/prometheus 3.14.0-1`, **packaged for aarch64**, 198 MiB installed. InfluxDB is the wrong
tool here and the friends' advice to drop it is right.

**Retention is a non-issue at this scale.** Rough arithmetic: ~10 smart home devices at ~6 series
each, plus router metrics, is on the order of **100-200 active series**. At a 60 s scrape that is
1440 samples/series/day, and Prometheus compresses to roughly 1.3-2 bytes/sample:

```
200 series x 1440 samples x 2 bytes  ~=  580 KB/day  ~=  210 MB/year
```

Ten years is about 2 GB. On the planned 2 TB `/var/data` that is effectively infinite. Set
`--storage.tsdb.retention.time=10y` (the default is 15 days, it **must** be changed).

Honest limitations, none of which bite at this size:
- Prometheus has **no downsampling**, so multi-year queries read every raw sample. Fine at 200
  series; it would not be at 200,000.
- Its TSDB is designed for operational monitoring, not archival. Back up via the admin snapshot API
  rather than copying the data directory live.
- If it is ever outgrown, **VictoriaMetrics** ingests Prometheus data and is a drop-in Grafana
  datasource. It is *not* packaged for aarch64 (only `aur/victoriametrics-bin`), which is the main
  reason it is not the first choice here.

### Dashboard: your own, served same-origin with Prometheus

**Grafana is not packaged for aarch64 at all**, not in the ALARM repos, not in the AUR (only
`grafana-alloy` / `grafana-agent`, which are collectors, not the dashboard). Running it would mean
the official ARM64 tarball with a hand-written unit, or a container, for ~200-400 MB RAM and a
separate update mechanism, to draw what may be four charts.

Prometheus instead exposes a plain JSON query API:

```
GET /api/v1/query_range?query=<promql>&start=<ts>&end=<ts>&step=<s>
```

**The one non-obvious problem, already solved in this stack:** a static page served on `:8080`
fetching `http://dragon:9090/api/...` is a *cross-origin* request, and Prometheus sends no CORS
headers, the browser silently blocks it. This trips people up constantly. `dashboard/nginx.conf`
therefore serves your files at `/` **and** reverse-proxies Prometheus at `/api/`, so everything is
same-origin and the JS is simply:

```js
fetch('/api/v1/query_range?query=fritz_homeauto_power_watt&start=...&end=...&step=60')
```

No CORS configuration, no credentials, no second hostname. `dashboard/html/index.html` currently
holds a placeholder that lists every `fritz_*` metric being produced, if that page shows metric
names, the whole pipeline works and only the charting is left to write.

## Why Docker here, when native was the earlier recommendation

The first assessment recommended native, and that was right *for the stack as it stood then*: two
daemons, one of them (`prometheus`) already packaged for aarch64. What changed is that the
dashboard is now **your own code with its own build and its own web server**. That makes it three
components, one of which is a deployment artefact rather than a package.

At that point Docker earns its keep:

- The dashboard needs a web server and a defined document root anyway. `nginx.conf` in this repo is
  reproducible; an nginx installed and hand-configured on dragon is not.
- The nginx-proxies-Prometheus arrangement above needs the two to share a network namespace with a
  stable name. Compose gives that for free; natively it means more config.
- It matches the workflow already in use on `wenlong:/var/docker/`, same compose shape, same
  `.env`, same instincts.

The overhead concern is real but small on this box: `dockerd` + `containerd` costs roughly
100-200 MB of dragon's 12 GB, on a CPU [idle 96-97 % of the time](../../docs/system.md#power-management).
Under 2 % of memory.

**Cost to be honest about:** installing Docker adds ~120 MB (`extra/docker`) plus
`docker-compose`, a daemon running as root, and a second update mechanism (image tags) alongside
`paru`. Both images are pinned in `docker-compose.yml` precisely so upgrades are a deliberate edit
rather than a surprise.

Both images publish `linux/arm64`, verified 2026-09-08 against Docker Hub.

## Deploying it

Nothing below has been run. Docker is not installed on dragon yet.

```bash
# on dragon, once:
paru -S docker docker-compose
sudo systemctl enable --now docker
sudo usermod -aG docker xuedi      # log out and back in

# copy this directory to dragon:/var/docker/dragon-dash/, then:
cp .env.example .env && $EDITOR .env      # FRITZ!Box user + password
mkdir -p data/prometheus
sudo chown -R 65534:65534 data/prometheus # prometheus runs as nobody
docker compose up -d
```

| Service | Bound to | Why |
|---|---|---|
| dashboard | `0.0.0.0:8080` | viewed from your desktop |
| prometheus | `127.0.0.1:9090` | admin API is enabled; keep it off the LAN |
| fritz-exporter | container network only | nothing outside needs it |

Prometheus UI from the desktop: `ssh -N -L 9090:127.0.0.1:9090 dragon`.

**Backup:** do not copy `data/prometheus` while it runs. Use the admin snapshot API
(`POST /api/v1/admin/tsdb/snapshot`), which is why `--web.enable-admin-api` is set, then archive the
snapshot directory. Once `/var/data` exists, backups belong there, never inside the stack, matching
the wenlong convention.

## Open questions before building

- **Which FRITZ!Box model and FRITZ!OS version?** Determines which TR-064 services exist.
- **Which smart home devices?** DECT 200/210 plugs, DECT 301/Comet thermostats, DECT 440 sensors.
  This decides which metrics are actually populated.
- **Scrape interval.** 60 s is ample for power and temperature; the storage maths above assumes it.
- **Where does the data live?** `/var/data` is the natural home, but it does not exist yet, see
  *External USB drive* in [`../../docs/system.md`](../../docs/system.md). Until that drive is working,
  Prometheus would have to store on the 238 GB boot NVMe, which is fine for the volumes involved.
- **A FRITZ!Box user with the right permissions**, and where its credentials are stored.

## Reference

- AHA-HTTP-Interface spec: <https://avm.de/fileadmin/user_upload/Global/Service/Schnittstellen/AHA-HTTP-Interface.pdf>
- `fritz_exporter`: <https://github.com/pdreker/fritz_exporter>
- Prometheus HTTP API: <https://prometheus.io/docs/prometheus/latest/querying/api/>

## The floor plan format

A JSON file, pointed at by `DD_SYSTEM_FRITZHOME_FLOORPLAN_FILE`. Plain SVG geometry so it can be
traced from a sketch by hand:

| Key | Meaning |
|---|---|
| `outline` | the flat's outer wall, an SVG polygon point string |
| `rooms[]` | `points` polygon plus a `name` and optional `labelX`/`labelY` |
| `walls[]` | interior walls, each an SVG polyline point string |
| `doors[]` | `x1,y1,x2,y2` segments, drawn over the walls they interrupt |
| `devices[]` | `ain` plus `x`/`y`, and an optional `label` overriding the device name |

Devices are placed by **AIN**, never by name, because names are not unique: a FRITZ!Smart Energy
250 reports as two entries sharing one name, distinguished only by an AIN suffix.

**Watch the polygon winding.** A room drawn with a concave step can exclude the pocket you meant to
include, which shows up as an unfilled white patch rather than an error. A point-in-polygon check on
a coordinate inside the questionable area is the quickest way to confirm the shape is what you
think.

Colour carries meaning: teal for power, indigo for temperature, red for doors and for a device the
box reports as absent.

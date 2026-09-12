# Releases and deployment

How a commit becomes a numbered release, and how that release ends up serving a dashboard on a
home server.

## Versioning

`internal/version/version.go` holds the single source of truth. Nothing is stamped in at link
time, so a binary built from a tarball, from CI or from a `go build` on a laptop all report the
same number. Two other places mirror it and must move in the same commit:

- the version badge in `README.md`
- the git tag, `v` plus the constant, which CI makes (see below)

Semantic versioning, pre-1.0: patch for a fix or a small change, minor for a notable feature, and
1.0 is a deliberate milestone rather than something that arrives by accretion.

The running binary reports it in two ways, which is what makes a deployed build identifiable
without checksums:

```
armdash -version
```

and the navbar sidebar, under the Build label.

## Releases

```mermaid
flowchart LR
    A[version.go + README badge bumped] --> B[push to main]
    B --> C[CI: gofmt, vet, tests, every target cross-compiled]
    C --> D{tag vX.Y.Z exists?}
    D -->|yes| E[nothing to do]
    D -->|no| F[tag vX.Y.Z]
    F --> G[GoReleaser]
    G --> H[tar.gz for every target]
    G --> I[deb, rpm, Arch packages per Linux arch]
    G --> J[checksums.txt]
    G --> K[GitHub release with a git changelog]
```

Every version that reaches `main` is released. CI's release job runs only on pushes to `main` and
only once the tests pass. It reads the version from `version.go`, refuses when the README badge
disagrees, and when that version has no tag yet it tags the commit and runs GoReleaser in the same
job. A tag pushed with the workflow's own token starts no other workflow, which is why tagging and
publishing cannot be split into two. A push that leaves the version alone releases nothing.

Bumping the version is therefore the release decision, and before 1.0 every change that ships gets
its own small release. Tags are never made by hand. If the job tags and then fails, the manual
Release workflow publishes the existing tag again.

`just release-snapshot` runs the whole pipeline locally into `./dist` without publishing, which is
the way to check a packaging change before tagging. It needs `goreleaser` on `PATH`.

## What a release contains

| Artifact | For |
|---|---|
| `armdash_<version>_<os>_<arch>.tar.gz` | the binary, README, licence, `.env.dist`, the unit file |
| `armdash_<version>_linux_<arch>.pkg.tar.zst` | Arch and Arch ARM |
| `armdash_<version>_linux_<arch>.deb` / `.rpm` | Debian, Ubuntu, Raspberry Pi OS, Fedora |
| `checksums.txt` | verifying any of the above |

| Platform | Architectures | Packages |
|---|---|---|
| Linux | `amd64`, `arm64`, `armv7`, `riscv64` | deb and rpm for all four, Arch for all but riscv64 |
| FreeBSD | `amd64`, `arm64` | tarball only |
| macOS | `amd64`, `arm64` | tarball only |

`armv7` is 32-bit ARM, such as Raspberry Pi OS 32-bit, and packages as `armhf`. The binary is
static and pure Go, so a target needs no toolchain, no runtime and no shared library beyond what a
bare system already has. The packages and the unit are systemd's; on FreeBSD and macOS the binary
runs the same, under whatever supervises services there.

## What the packages install

```
/usr/bin/armdash                          the binary
/usr/lib/systemd/system/armdash.service   the unit, shipped disabled
/usr/share/armdash/armdash.env.example    the seed configuration
/etc/armdash/armdash.env                  0640 root:armdash, seeded on first install
/var/lib/armdash/                         0700 armdash, created by systemd on start
```

The post-install creates the `armdash` system user, seeds the configuration only when there is
not one already (an upgrade must never drop credentials) and leaves the unit disabled, because a
dashboard with no FRITZ!Box credentials and no Prometheus address is not worth starting. Its next
steps include `armdash passwd`, which prints the owner login for the env file, see
[authentication.md](authentication.md). Without it the dashboard runs, but nothing can be changed.

`just install` does the same from a checkout on the machine itself: it builds, installs the binary
to `/usr/local/bin`, creates the user, seeds the env file when there is none and installs the unit
with its `ExecStart` pointed there, disabled. It needs sudo.

The unit is hardened further than most, and can be, because **the app writes almost nothing**.
Configuration is read-only by design and every metric lives in Prometheus. The one writable path is
`/var/lib/armdash`, from `StateDirectory=`, where an uploaded floor plan and device positions
are kept (see [floorplan.md](floorplan.md)). `ProtectSystem=strict` keeps everything else read-only.
The one capability granted is `CAP_NET_BIND_SERVICE`, which is what lets an unprivileged process
answer on ports 80 and 443.

`StateDirectory=` arrived in 0.10.0. A package upgrade brings the new unit; swapping only the binary
does not, and until the unit is updated (and `systemctl daemon-reload` run) the floor plan page
simply offers no upload and no edit mode.

The login arrived in 0.11.0. An upgrade from an earlier version keeps every page, but loses Upload
and Edit until `AD_CORE_AUTH_USER` and `AD_CORE_AUTH_PASSWORD_HASH` are in the env file.

## Configuration on a server

Everything is in `/etc/armdash/armdash.env`, in the same `AD_` variables the development
`.env.local` uses. `AD_CORE_ADDR` decides the port, so moving the dashboard to another port is an
edit and a restart, not a rebuild. See [configuration.md](configuration.md).

The committed defaults stay on high loopback ports, `127.0.0.1:9494` and `127.0.0.1:9495` for
HTTPS, so a development checkout never collides with anything else on the machine. Ports 80 and
443 only ever appear in the server's env file.

## HTTPS

armdash terminates TLS itself, there is no proxy to run. Set both paths and it serves the
dashboard on `AD_CORE_TLS_ADDR` as well:

```ini
AD_CORE_ADDR=:80
AD_CORE_TLS_ADDR=:443
AD_CORE_TLS_CERT=/etc/armdash/tls/armdash.crt
AD_CORE_TLS_KEY=/etc/armdash/tls/armdash.key
```

With TLS on, the plain listener keeps running but redirects every request to HTTPS, **except
`/metrics`**. Prometheus scrapes that over plain loopback HTTP as before, so its configuration does
not change and it never has to trust the certificate. Setting only one of the two paths refuses to
start rather than quietly staying on HTTP.

The certificate comes from whatever CA the browsers on the network already trust, for a LAN host
name typically a private one. Every device that opens the dashboard needs that CA imported, which
is also why there is **no `Strict-Transport-Security` header**: a long-lived pin on a LAN host name
would lock out any device without the CA, and every other plain-HTTP service on the same name.

The service user reads the pair, nobody else reads the key:

```
/etc/armdash/tls/              0750 root:armdash
/etc/armdash/tls/armdash.crt   0644 root:armdash
/etc/armdash/tls/armdash.key   0640 root:armdash
```

The pair is read once at startup, like the rest of the configuration, so a renewed certificate
takes effect on the next restart. The unit needs no change: `ProtectSystem=strict` still allows
reading `/etc`, and `CAP_NET_BIND_SERVICE` covers port 443 as it does port 80. The Settings page
shows which certificate and key are in use.

## The other half: Prometheus

armdash reads everything from Prometheus and stores nothing itself, so a deployment is really
three services:

```mermaid
flowchart LR
    NE[node-exporter :9100] -->|scrape| P[(Prometheus :9090)]
    DD[armdash /metrics] -->|scrape| P
    FB[FRITZ!Box AHA API] -->|poll| DD
    P -->|query| DD2[armdash pages :80 / :443]
```

armdash appears twice on purpose. It polls the FRITZ!Box and republishes what it finds on
`/metrics` for Prometheus to scrape, and it queries Prometheus to draw the pages. That is why the
FRITZ!Box credentials exist in exactly one place and there is no separate exporter to keep alive.

Prometheus and node_exporter are packaged by the major distributions on ARM and x86 alike (on
Arch `extra/prometheus` and `extra/prometheus-node-exporter`), so a native install needs no
containers. `deploy/` also holds a
compose file for hosts where containers are preferred.

Two Prometheus flags matter:

- `--storage.tsdb.retention.time=10y`, because the default is 15 days and indefinite history is the
  entire point of the project.
- `--web.enable-admin-api`, if the snapshot backup path is wanted. It also means Prometheus should
  listen on loopback only.

### Storage

Measured on a deployed instance, not estimated: node_exporter, armdash and Prometheus itself
come to **1663 active series** together on a small board, of which node_exporter is 834 and the
FRITZ!Box data 45. At a 60 s scrape that is ~28 samples/s, and Prometheus compresses to roughly
1.7 bytes/sample:

```
28 samples/s x 1.7 bytes  ~=  4 MB/day  ~=  1.5 GB/year
```

A decade fits in under 20 GB. Any modern root filesystem holds that, so the TSDB does not need a
dedicated data disk, and putting it on one is a preference rather than a requirement. A bigger host
reports more series (a desktop's node_exporter alone reports about 1100), but the order of magnitude
does not change.

## Renamed from dragon-dash

Up to 0.11.0 the project was called dragon-dash. 0.12.0 renamed everything that carried the old
name:

| Up to 0.11.0 | From 0.12.0 |
|---|---|
| package, binary, unit, system user `dragon-dash` | `armdash` |
| `/etc/dragon-dash/dragon-dash.env` | `/etc/armdash/armdash.env` |
| `/etc/dragon-dash/tls/dragon-dash.crt` and `.key` | `/etc/armdash/tls/armdash.crt` and `.key` |
| `/var/lib/dragon-dash/` | `/var/lib/armdash/` |
| every `DD_` setting | the same name with `AD_` |
| `DD_SYSTEM_DRAGON_*`, pages under `/s/dragon/` | `AD_SYSTEM_HOST_*`, pages under `/s/host/` |
| metric `dragon_dash_collector_up` | `armdash_collector_up` |

The armdash packages replace and conflict with dragon-dash, so installing one removes the other.
Nothing is migrated automatically and the old `DD_` names are not read any more, so an existing
install moves once, by hand:

1. Stop and disable `dragon-dash`.
2. Copy the env file to `/etc/armdash/armdash.env` **before** installing, so the post-install keeps
   it rather than seeding a fresh one. Rename every key from `DD_` to `AD_`, `DD_SYSTEM_DRAGON_` to
   `AD_SYSTEM_HOST_`, and point the TLS paths at their new place.
3. Move the certificate and key to `/etc/armdash/tls/`, and `/var/lib/dragon-dash` to
   `/var/lib/armdash`. systemd hands the state directory to the new user on the first start.
4. Install the package and `systemctl enable --now armdash`.
5. Update whatever refers to the old names on the Prometheus side (a scrape job named after the
   service, a query on the collector metric), then remove the `dragon-dash` user and group.

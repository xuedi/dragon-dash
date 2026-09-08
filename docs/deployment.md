# Releases and deployment

How a commit becomes a numbered release, and how that release ends up serving a dashboard on a
home server.

## Versioning

`internal/version/version.go` holds the single source of truth. Nothing is stamped in at link
time, so a binary built from a tarball, from CI or from a `go build` on a laptop all report the
same number. Two other places mirror it and must move in the same commit:

- the version badge in `README.md`
- the git tag, which is `v` plus the constant

Semantic versioning, pre-1.0: patch for a fix or a small change, minor for a notable feature, and
1.0 is a deliberate milestone rather than something that arrives by accretion.

The running binary reports it in two ways, which is what makes a deployed build identifiable
without checksums:

```
dragon-dash -version       # 0.5.0
```

and the navbar sidebar, under the Build label.

## Cutting a release

```mermaid
flowchart LR
    A[version.go + README badge bumped] --> B[just release 0.5.0]
    B --> C{guards}
    C -->|version.go matches| D[git tag v0.5.0]
    C -->|README badge matches| D
    C -->|on main, tree clean| D
    D --> E[push tag]
    E --> F[Release workflow]
    F --> G[GoReleaser]
    G --> H[tar.gz, amd64 + arm64]
    G --> I[deb, rpm, Arch packages]
    G --> J[checksums.txt]
    G --> K[GitHub release with a git changelog]
```

`just release VERSION` refuses to tag when the constant, the badge, the branch or a dirty tree
disagree with what is being released. It only tags and pushes; everything after that happens in
CI, so a release cannot depend on what happens to be installed on one machine.

`just release-snapshot` runs the whole pipeline locally into `./dist` without publishing, which is
the way to check a packaging change before tagging. It needs `goreleaser` on `PATH`.

## What a release contains

| Artifact | For |
|---|---|
| `dragon-dash_<version>_linux_arm64.tar.gz` | the binary, README, licence, `.env.dist`, the unit file |
| `dragon-dash_<version>_linux_amd64.tar.gz` | same, for an x86 host |
| `dragon-dash-<version>-*.pkg.tar.zst` | Arch and Arch ARM |
| `dragon-dash_<version>_*.deb` / `.rpm` | Debian, Ubuntu, Fedora |
| `checksums.txt` | verifying any of the above |

The binary is static and pure Go, so the target needs no toolchain, no runtime and no shared
library beyond what a bare system already has.

## What the packages install

```
/usr/bin/dragon-dash                          the binary
/usr/lib/systemd/system/dragon-dash.service   the unit, shipped disabled
/usr/share/dragon-dash/dragon-dash.env.example the seed configuration
/etc/dragon-dash/dragon-dash.env              0640 root:dragon-dash, seeded on first install
```

The post-install creates the `dragon-dash` system user, seeds the configuration only when there is
not one already (an upgrade must never drop credentials) and leaves the unit disabled, because a
dashboard with no FRITZ!Box credentials and no Prometheus address is not worth starting.

The unit is hardened further than most, and can be, because **the app writes nothing**.
Configuration is read-only by design and every metric lives in Prometheus, so `ProtectSystem=strict`
needs no `ReadWritePaths` exception at all. The one capability granted is `CAP_NET_BIND_SERVICE`,
which is what lets an unprivileged process answer on port 80.

## Configuration on a server

Everything is in `/etc/dragon-dash/dragon-dash.env`, in the same `DD_` variables the development
`.env.local` uses. `DD_CORE_ADDR` decides the port, so moving the dashboard to another port is an
edit and a restart, not a rebuild. See [configuration.md](configuration.md).

## The other half: Prometheus

dragon-dash reads everything from Prometheus and stores nothing itself, so a deployment is really
three services:

```mermaid
flowchart LR
    NE[node-exporter :9100] -->|scrape| P[(Prometheus :9090)]
    DD[dragon-dash /metrics] -->|scrape| P
    FB[FRITZ!Box AHA API] -->|poll| DD
    P -->|query| DD2[dragon-dash pages :80]
```

dragon-dash appears twice on purpose. It polls the FRITZ!Box and republishes what it finds on
`/metrics` for Prometheus to scrape, and it queries Prometheus to draw the pages. That is why the
FRITZ!Box credentials exist in exactly one place and there is no separate exporter to keep alive.

Prometheus and node_exporter are both packaged for aarch64 (`extra/prometheus`,
`extra/prometheus-node-exporter`), so a native install needs no containers. `deploy/` also holds a
compose file for hosts where containers are preferred.

Two Prometheus flags matter:

- `--storage.tsdb.retention.time=10y`, because the default is 15 days and indefinite history is the
  entire point of the project.
- `--web.enable-admin-api`, if the snapshot backup path is wanted. It also means Prometheus should
  listen on loopback only.

### Storage

Measured on a running instance, not estimated: node_exporter, dragon-dash and Prometheus itself
come to about **2100 active series** together, of which node_exporter is roughly 1100 and the
FRITZ!Box data about 45. At a 60 s scrape that is ~35 samples/s, and Prometheus compresses to
roughly 1.7 bytes/sample:

```
35 samples/s x 1.7 bytes  ~=  5 MB/day  ~=  1.8 GB/year
```

A decade fits in under 20 GB. Any modern root filesystem holds that, so the TSDB does not need a
dedicated data disk, and putting it on one is a preference rather than a requirement.

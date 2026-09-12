# Systems

A **system** is one entry in the top navigation bar: Host, FritzHome, and whatever is added
later. Systems are the unit of extension, and the shell has no knowledge of any individual one.

The navbar can also carry links to other sites. Those are configuration, not systems, and are
described in [links.md](links.md).

## The contract

`internal/system/system.go` defines it:

```go
type System interface {
    ID() string                    // stable, URL-safe: "host"
    Title() string                 // navbar label: "Host"
    Nav() []NavItem                // left sidebar, as data
    ConfigSchema() []ConfigField   // settings page generates itself from this
    Render(slug string, r *http.Request) (template.HTML, error)
    Register(mux *http.ServeMux, prefix string, deps Deps)
}
```

Everything the shell needs in order to draw itself is returned as **data**. Navigation is not
hardcoded anywhere; neither is the settings form. Adding a system requires no change in
`internal/server`.

`Render` returns a page body only. A system never emits `<html>` or navigation. The shell wraps
the fragment in the layout, Each system owns its own templates through its own `embed.FS`, which is
what keeps them genuinely independent.

`Register` gives a system a subtree at `/s/<id>/api/` for htmx fragments and JSON. Both systems'
chart data comes from there, served by the shared engine in `internal/chart`; FritzHome's floor plan
upload goes there too. The shell wraps the
whole subtree: a disabled system's endpoints answer 404, and every state-changing request needs the
owner's session and passes Go's `http.CrossOriginProtection`, so a write endpoint is protected
without the system doing anything. A page asks `system.CanEdit(r)` whether to draw its edit
controls, so a new system gets the login for free. See [authentication.md](authentication.md).

`Deps.DataDir` is the system's own directory for what people change through a page, empty when no
data directory is configured. A system that writes offers nothing to change when it is empty. See
[configuration.md](configuration.md).

The ID and the title are deliberately separate. The ID is a stable identifier that ends up in URLs
and in settings such as `AD_SYSTEM_HOST_ENABLED`. The title is a label, and can be reworded
whenever it reads better without breaking either.

## Registration

Systems register themselves from `init()` and are blank-imported in `cmd/armdash/main.go`:

```go
func init() { system.Register(&Host{}) }
```

Everything compiled in is *available*; the config decides what is *shown*. A disabled system
disappears from the navbar and its routes return 404. See [configuration.md](configuration.md).

## Why not runtime plugins

Go's `plugin` package requires an identical toolchain and identical dependency versions between
host and plugin, does not work on Windows, and cannot cross-compile. Since this binary is built on
a desktop and run on an arm64 board, that rules it out completely.

The alternative, out-of-process plugins over RPC, works well but means several binaries instead of
one. The single binary is the property that makes this project pleasant to deploy.

So: compile-time registration. The interface above is deliberately narrow and free of Go-native
callbacks, so if third-party plugins ever earn their keep, it is the seam they would go through
without changing how existing systems are written.

## Collecting metrics

A system may also implement `system.Collector`:

```go
type Collector interface {
    Collect(ctx context.Context) ([]Metric, error)
}
```

The shell then exposes it at `/metrics` in the Prometheus text format. This is what lets
armdash gather *and* display the same data without a separate exporter process: FritzHome polls
the box, the page shows the live reading, and Prometheus scrapes the identical reading for history.

A collector that fails does not fail the scrape. The shell emits
`armdash_collector_up{system="..."} 0` instead, so Prometheus records that the collector is
down rather than simply showing a gap.

## Adding one

1. Create `internal/systems/<name>/`, implement the interface, call `system.Register` in `init()`.
2. Blank-import the package in `cmd/armdash/main.go`.

There is no third step.

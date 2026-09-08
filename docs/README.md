# dragon-dash architecture

One file per logical chunk of the application. These describe **how it works**, not what the code
says, read the relevant one before working in that area.

| Document | Covers |
|---|---|
| [systems.md](systems.md) | The `System` interface, the registry, how the shell builds its navigation |
| [configuration.md](configuration.md) | The config file, key namespacing, how the settings page generates itself |
| [prometheus.md](prometheus.md) | Why every metric comes from Prometheus, the client, query conventions |
| [frontend.md](frontend.md) | Templates, Bulma, htmx, uPlot, and why there is no node toolchain |
| [fritzbox-metrics.md](fritzbox-metrics.md) | Which FRITZ!Box exporter and why, storage sizing, rejected alternatives |

# Prometheus

Server metrics come from Prometheus. dragon-dash reads `/proc` for nothing and stores no samples of
its own.

Smart home data is the exception in one direction only: dragon-dash polls the FRITZ!Box itself and
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

Server metrics assume `prometheus-node-exporter`. Smart home metrics are **discovered** rather than
hardcoded, because exact names depend on the exporter version and which devices are paired. See
[fritzbox-metrics.md](fritzbox-metrics.md).

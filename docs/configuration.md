# Configuration

One JSON file, `config.json` by default, overridable with `-config`. Written atomically (temp file
plus rename, so an interrupted write cannot truncate a good config) with mode `0600`, because it
can hold credentials.

## Why not a database

Everything stored here is configuration, a handful of strings. A plain file is transparent,
trivially backed up, editable by hand when something is wrong, and keeps the binary on the standard
library alone. Actual data belongs in Prometheus, which is already a time-series database and far
better at it.

## Key namespacing

```
core.prometheus_url            shell-owned
system.<id>.enabled            per system, "1" or "0"
system.<id>.<field>            per system, from its ConfigSchema
```

A system receives a `Scope`, not the whole config, so it can only read and write its own namespace.
One system cannot read another's secrets by accident.

**An unset `enabled` key means enabled.** A fresh install shows every system rather than presenting
an empty shell with no clue what to do.

## The settings page generates itself

The shell walks every registered system, asks for its `ConfigSchema()`, and renders an input per
field, text, password, url or checkbox. A system never writes settings UI.

Two details worth knowing when editing that code:

- Unchecked checkboxes are **absent** from an HTML form body, not sent as `false`. Enablement is
  therefore derived from presence (`r.Form.Has`), never from a value.
- The settings page lists **all** registered systems, not only the enabled ones. Otherwise
  switching a system off would remove the control needed to switch it back on.

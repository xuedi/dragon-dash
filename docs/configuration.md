# Configuration

Read-only, loaded once at startup from env files and the environment. Nothing in it can be changed
through the web interface.

That is the central design decision, not an omission. With no way to change configuration there is
no form that can point the application somewhere else or read a credential back out, not even for
someone logged in.

## Data is not configuration

Two things *are* changed through a page: the FritzHome floor plan upload and the device positions,
see [floorplan.md](floorplan.md). They are data, and they live apart from configuration, in a data
directory: `DD_CORE_DATA_DIR`, falling back to systemd's `$STATE_DIRECTORY`. Each system gets its
own subdirectory. Without a data directory, no page offers to change anything.

Changing that data needs the login, see [authentication.md](authentication.md). Every system
endpoint under `/s/<id>/api/` also goes through Go's `http.CrossOriginProtection`, which rejects a
state-changing request a browser marks as coming from another site, so a page elsewhere on the web
cannot make a logged-in browser do it either.

## The login

`DD_CORE_AUTH_USER` and `DD_CORE_AUTH_PASSWORD_HASH` name the one user who may change anything.
`dragon-dash passwd` prints both. They are configuration like the rest, read at startup, so the
password changes by editing the file and restarting, never through a page. Without them nothing can
be changed. Details in [authentication.md](authentication.md).

## Sources

Applied in order, each overriding the last:

| Source | Purpose |
|---|---|
| `.env.dist` | committed defaults, safe to read |
| `.env.local` | gitignored, `0600`, where credentials belong |
| environment | wins over both, so containers and systemd units need no files |

Override the file list with `-env a.env,b.env`. A missing file is not an error: a container may
configure everything through real environment variables.

## Naming

Every variable starts with `DD_`. A dotted key maps to it by uppercasing and replacing separators:

```
core.prometheus_url                DD_CORE_PROMETHEUS_URL
core.tls_cert                      DD_CORE_TLS_CERT
core.auth_user                     DD_CORE_AUTH_USER
system.fritzhome.password          DD_SYSTEM_FRITZHOME_PASSWORD
system.dragon.enabled              DD_SYSTEM_DRAGON_ENABLED
link.wiki.url                      DD_LINK_WIKI_URL
```

Navbar links have their own `link.<id>.` namespace next to `core.` and `system.`, see
[links.md](links.md).

A variable that does not start with `DD_` is rejected at load with the file and line number.
Silently ignoring `PROMETHEUS_URL=` because of a missing prefix is a miserable thing to debug.

**An unset `enabled` key means enabled.** A fresh checkout shows every system rather than an empty
shell with no clue what to do.

## Scopes

A system receives a `Scope`, never the whole config, so it can only read its own namespace. One
system cannot read another's credentials by accident. The scope has no setter.

## The settings page

Read-only by construction. It reports each value in effect, the environment variable it came from,
and **which source won**, because "why is this empty" is nearly always answered by discovering the
value came from `.env.dist` rather than `.env.local`.

Once a login is configured the page is only shown to whoever is logged in, since it names hosts and
users. Without one it stays open, so a fresh install can still be inspected. Either way fields marked
`Secret` in a system's `ConfigSchema` are shown as dots, and the login's own hash is only reported as
set: nothing that works as a credential is ever rendered.

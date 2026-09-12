# Configuration

Read-only, loaded once at startup from env files and the environment. Nothing can be changed
through the web interface.

That is the central design decision, not an omission. With no write path there is no form to
protect, no CSRF surface and no way for a visitor to point the application at somewhere else. It is
what makes running on a LAN without a login defensible.

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

Fields marked `Secret` in a system's `ConfigSchema` are shown as dots. The page is reachable without
authentication, so a password must never be rendered.

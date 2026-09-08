# Frontend

Server-rendered `html/template`, Bulma for styling, htmx for interaction, uPlot for charts. There
is no `package.json` and no build step.

## No node toolchain

Bulma, htmx and uPlot are **committed as files** in `web/static/` and embedded with `go:embed`.
Upgrading one means downloading a file and committing it, a visible, reviewable change rather than
a lockfile diff.

This costs the conveniences of a bundler. It buys a build that is `go build`, a CI job with one
step, and a project that will still compile untouched in five years.

## Layout and fragments

`web/templates/layout.html` is the shell: navbar, sidebar, content slot. Systems return page
*bodies* as `template.HTML`, which the shell inserts. Each system parses its own templates from its
own `embed.FS`, so template names cannot collide between systems.

A system whose `Render` fails does not take the page down: the shell renders anyway and puts the
error in a notification above the content. Losing the navigation because one panel broke would be
much worse than seeing the error.

## Charts: uPlot, not Chart.js

A year of data at 60-second resolution is roughly 500,000 points. uPlot is built for that volume in
about 50 KB; Chart.js would struggle. Chart data is fetched as JSON from the owning system's
`/s/<id>/api/` subtree.

## Theming

Bulma 1.0 exposes CSS custom properties (`--bulma-border`, `--bulma-text-weak`, and so on). The
floor plan SVG uses those rather than fixed colours, so it follows the theme instead of fighting it.

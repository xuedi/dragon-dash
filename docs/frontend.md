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

## Page shape

Every page is assembled from stock Bulma, in the same order, so no page invents its own header:

```
navbar has-shadow  >  container
section > container > columns
    column is-2   aside.menu   sidebar, contributed by the active system
    column is-10  page body
```

The body always opens with the **page-top infobar**: a `box` containing a `level`, with the page
title and context items separated by pipes on the left, and actions on the right. It is built in Go
as a `system.PageTop`, not in the template, so a page cannot drift into a bespoke header.

Action order, left to right: state-changing buttons first, filters next, navigation last. On a
chart page that means the sample count, then the range selector.

## Custom CSS

Ten lines, all of them Bulma's own custom properties (`--bulma-family-primary`, `--bulma-radius`,
`--bulma-body-background-color`). There is not a single selector override, so a Bulma upgrade cannot
silently break the layout.

Spacing comes from `section`, `container` and `columns`; emphasis comes from helper classes
(`has-text-weight-semibold`, `has-text-link`, `is-size-7`, `has-text-grey`). If the answer to "which
Bulma class does this" is "there is one", the rule does not get written.

One consequence worth knowing: `navbar-item.is-active` paints a solid block, which is too heavy for
a light bar, so the active top-level item uses `has-text-weight-semibold has-text-link` instead.

## Chart resizing

uPlot sizes its canvas once at construction, so it has to be told when its column changes width.
Two triggers, deliberately:

- `window.addEventListener('resize', ...)` is the mechanism. It fires reliably and covers the
  ordinary case.
- A `ResizeObserver` on the chart container supplements it, catching layout changes that move the
  column without moving the window.

The observer is not enough on its own: its callbacks are delivered during the rendering steps, so a
page that is not actively painting (an offscreen or background tab) receives none. That is also why
it cannot be verified by resizing a headless window. Test it by changing the container width and
dispatching a `resize` event.

Both go through one `ddResize()` with a **width guard**. Resizing the canvas can itself trigger the
observer, and without the guard the two feed each other into a loop.


## Light and dark

Bulma 1.0 ships both palettes and switches between them on `prefers-color-scheme`, so the dashboard
follows the browser with no toggle and no preference to store. Everything the framework draws,
boxes, tables, the menu, tags, comes along for free.

That freedom has one condition: **never give a colour a literal value.** A hardcoded light shade sits
outside the media query, so it survives the switch while everything around it flips, and the result
is a white page behind black boxes. The rule is to set Bulma's own custom properties to Bulma's own
scheme colours, `var(--bulma-scheme-main-bis)` rather than the hsl triple it happens to resolve to
today.

Two places cannot inherit the palette and need explicit help:

- **The floor plan.** SVG presentation attributes do not accept `var()`, so the plan carries a small
  set of classes (`.fp-room`, `.fp-wall`, `.fp-label`) whose rules are Bulma variables. Device
  markers keep their semantic colours, which read on either background.
- **The charts.** uPlot paints a canvas, which inherits nothing. The axis and grid colours are read
  out of the computed style at draw time, and a `prefers-color-scheme` listener redraws from the
  cached payload, because a canvas keeps the colours it was drawn with.

To check a change, force the palette rather than trusting the machine you are on:

```js
document.documentElement.dataset.theme = 'dark';   // 'light', or delete to follow the browser
```

Bulma keys its dark rules off both the media query and `[data-theme]`, so this exercises exactly the
variables a dark browser would use. Comparing computed styles in both states catches the literal
colours a screenshot on a light machine never will.

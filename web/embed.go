// Package web holds the shell's templates and the vendored frontend assets.
//
// Everything is embedded, so the result is one binary with no runtime files and
// no node toolchain: Bulma, htmx and uPlot are committed as plain files rather
// than pulled by npm at build time.
package web

import "embed"

//go:embed templates/*.html
var Templates embed.FS

//go:embed static
var Static embed.FS

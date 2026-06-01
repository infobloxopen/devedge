package helm

import "embed"

// chartsFS holds the in-repo Helm charts devedge installs for dependency
// runtime. The `all:` prefix is required so template helpers like
// `_helpers.tpl` (leading underscore) are embedded rather than silently
// excluded by the default go:embed rules.
//
//go:embed all:charts
var chartsFS embed.FS

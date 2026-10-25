package webui

import "embed"

// Static bundles the Vue application, as in space-merge.
//
// Include Vite helper chunks whose names start with an underscore.
//go:embed all:static
var Static embed.FS

// Package assets embeds the templates directory into the binary.
package assets

import "embed"

//go:embed templates/*
var FS embed.FS

//go:embed dashboards/*
var DashboardsFS embed.FS

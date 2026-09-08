// Package web embeds the dashboard UI.
package web

import _ "embed"

// Index is the single-page dashboard served at /.
//
//go:embed index.html
var Index []byte

// Package web embeds the dashboard UI.
package web

import _ "embed"

// Index is the single-page dashboard served at /.
//
//go:embed index.html
var Index []byte

// SmoothieJS is the pinned Smoothie Charts 1.36.1 runtime used by the Pulse
// monitor. It is served locally so the dashboard works without public internet.
//
//go:embed vendor/smoothie-1.36.1.js
var SmoothieJS []byte

//go:build tools

// Package tools anchors bootstrap dependencies that are not application code yet.
package tools

import (
	_ "github.com/LadybugDB/go-ladybug"
	_ "modernc.org/sqlite"
)

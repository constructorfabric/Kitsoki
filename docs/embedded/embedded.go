// Package embedded ships kitsoki's CLI-embedded reference docs.
// The files in this directory are compiled into the `kitsoki` binary
// via //go:embed so `kitsoki docs <topic>` works without a repo checkout.
package embedded

import "embed"

//go:embed *.md
var FS embed.FS

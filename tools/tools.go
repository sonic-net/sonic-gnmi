//go:build tools
// +build tools

// This file keeps x/term in go.mod so go mod tidy does not drop it.
// The gnmi_cli patch (patches/0001-Updated-to-filter-and-write-to-file.patch)
// adds an import of golang.org/x/crypto/ssh/terminal, which in x/crypto v0.24.0+
// depends on golang.org/x/term. Because the patch is applied after go mod vendor,
// this indirect dependency must be explicitly retained here.
package tools

import _ "golang.org/x/term"

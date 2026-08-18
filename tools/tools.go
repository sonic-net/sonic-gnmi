// Package tools retains build-time-only dependencies in go.mod.
// The gnmi_cli patch (patches/0001-Updated-to-filter-and-write-to-file.patch)
// adds an import of golang.org/x/crypto/ssh/terminal, which in x/crypto v0.24.0+
// depends on golang.org/x/term. Since this import is only added by a post-vendor
// patch (not present in the original source), go mod tidy would otherwise strip
// golang.org/x/term from go.mod.
package tools

import _ "golang.org/x/term"

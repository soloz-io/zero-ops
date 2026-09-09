// Package embedded carries the platform content a released CLI needs at Day-0.
//
// The tree below is copied from the repository by
// scripts/package/embed-platform-assets.sh and verified by a pre-commit hook, so
// it is generated content rather than a second place to edit a manifest. Change
// the manifest; the copy follows.
package embedded

import "embed"

// FS holds the copied tree, addressed by repository-relative path so a caller
// cannot tell an embedded read from a working-tree one.
//
//go:embed all:manifests
var FS embed.FS

// Package platform resolves the platform content the CLI needs at Day-0.
//
// ADR-063 requires a release to carry a build a tenant can run. That was untrue:
// bootstrap read manifests/ from the working directory in seven places, so the
// published binary only worked inside a checkout of zero-ops. A tenant obtaining
// the CLI from a release could not use it, and the custody claim -- that a box is
// installable from artefacts the tenant holds -- was false for the one artefact
// the tenant runs first.
//
// The rule is ADR-068's, applied to content rather than to versions: an
// unreleased build reads the working tree, a released build reads what it
// carries. A development build must keep reading the tree, or a change to a
// manifest would not take effect until it was published.
package platform

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/versions"
	"github.com/soloz-io/zero-ops/internal/platform/embedded"
)

// ReadFile returns platform content by its repository-relative path, for
// example "manifests/environments/base/hubenvironment.yaml".
//
// The path is the same in both modes deliberately: a caller cannot tell which
// build it is running in, so it cannot acquire a dependency on one.
func ReadFile(path string) ([]byte, error) {
	// An absolute path is not platform content. It is a caller naming a
	// specific file -- a test fixture, an operator pointing at a template of
	// their own -- and there is no embedded tree it could refer to.
	if filepath.IsAbs(path) {
		return os.ReadFile(path)
	}
	if versions.IsReleaseBuild() {
		b, err := embedded.FS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("release %s carries no %s: the binary is "+
				"incomplete and cannot bootstrap without a checkout, which is the "+
				"dependency releases exist to remove (ADR-063): %w",
				versions.BundleVersion, path, err)
		}
		return b, nil
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, path))
}

// ReadDir lists a directory of platform content, sorted, names only.
func ReadDir(path string) ([]fs.DirEntry, error) {
	if filepath.IsAbs(path) {
		return os.ReadDir(path)
	}
	if versions.IsReleaseBuild() {
		return embedded.FS.ReadDir(path)
	}
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return os.ReadDir(filepath.Join(root, path))
}

// Exists reports whether platform content is present, without reading it.
func Exists(path string) bool {
	if filepath.IsAbs(path) {
		_, err := os.Stat(path)
		return err == nil
	}
	if versions.IsReleaseBuild() {
		f, err := embedded.FS.Open(path)
		if err != nil {
			return false
		}
		_ = f.Close()
		return true
	}
	root, err := os.Getwd()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(root, path))
	return err == nil
}

// WalkDir walks platform content, so a caller written against it works
// identically on an embedded tree and a working tree.
func WalkDir(root string, fn fs.WalkDirFunc) error {
	if filepath.IsAbs(root) {
		return filepath.WalkDir(root, fn)
	}
	if versions.IsReleaseBuild() {
		return fs.WalkDir(embedded.FS, root, fn)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Paths are handed back repository-relative in both modes, so a caller
	// cannot tell which it is walking.
	return filepath.WalkDir(filepath.Join(wd, root), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(wd, p)
		if relErr != nil {
			return relErr
		}
		return fn(rel, d, nil)
	})
}

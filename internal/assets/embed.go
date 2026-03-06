package assets

import (
	"embed"
)

//go:embed manifests
var manifestsFS embed.FS

//go:embed catalog
var catalogFS embed.FS

// ReadManifest reads a manifest file from the embedded manifests directory
func ReadManifest(path string) ([]byte, error) {
	return manifestsFS.ReadFile("manifests/" + path)
}

// ReadCatalog reads a catalog file from the embedded catalog directory
func ReadCatalog(path string) ([]byte, error) {
	return catalogFS.ReadFile("catalog/" + path)
}

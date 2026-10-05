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

// EncryptionSecretName is the Secret a control plane reads its at-rest encryption
// provider configuration from, for one cluster.
//
// IT EXISTS SO THERE IS EXACTLY ONE SPELLING. The name appears in three places that
// cannot see each other: this file, the Day-0 Secret template rendered by the
// provisioner and by `soloz encryption enable`, and the `secretEncryptionConfig`
// patch in each ClusterClass, which builds it from `{{ .builtin.cluster.name }}`
// inside CAPI at topology-reconcile time. A mismatch between the Go half and the
// CAPI half produces a control-plane node referencing a Secret that does not exist,
// and CAPI reports that as a bootstrap that never completes rather than as a name it
// could not find. Preflight 003-encryption-secret-is-per-cluster asserts the two halves agree.
//
// The key is per cluster because ADR-100 decides it is and the escrow holds it under
// the cluster's id (ADR-076). An earlier version named one flat Secret for every
// cluster of a class, which is the defect this function's existence prevents.
func EncryptionSecretName(cluster string) string {
	return cluster + "-encryption-config"
}

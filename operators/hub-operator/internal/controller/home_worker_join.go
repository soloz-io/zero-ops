package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Home worker join flow for the hybrid provider cell (ADR-046 §WS4).
//
// For each hybrid SpokePool with home-worker-enabled=true, the hub-operator:
//  1. Reads the spoke kubeconfig Secret (<spoke>-kubeconfig, platform-capi) that
//     CAPI generates for the spoke cluster.
//  2. Computes the spoke's CA certificate sha256 (discovery-token-ca-cert-hash).
//  3. Mints a kubeadm bootstrap token Secret (bootstrap.kubernetes.io/token) in
//     the spoke's kube-system namespace (pure-API, no kubeadm binary required).
//  4. Writes the join payload Secret (<spoke>-home-worker-join, platform-capi)
//     consumed by scripts/hybrid/home-worker-join.sh.
//  5. Rotates the token before expiry (rotation window = TTL/2) and cleans up
//     expired tokens for the same spoke.
//
// The join flow is unmanaged by design: home WSL2 workers are convenience nodes
// that join via a plain `kubeadm join` with a minted bootstrap token. They never
// enter the CAPI topology (the ClusterClass stays CAPI-managed).

// bootstrapTokenSecretType is the kubeadm bootstrap-token Secret type.
const bootstrapTokenSecretType = "bootstrap.kubernetes.io/token"

// bootstrapTokenTTLAnnotation is the claim annotation carrying the TTL (e.g. "24h").
const bootstrapTokenTTLAnnotation = "home-worker-ttl"

// homeWorkerEnabledAnnotation is the claim annotation gating the join flow.
const homeWorkerEnabledAnnotation = "home-worker-enabled"

// homeWorkersAnnotation is the claim annotation carrying the home worker list.
// Format: JSON array of objects with at least a "hostname" field.
const homeWorkersAnnotation = "home-workers"

// joinSecretSuffix is the suffix of the join payload Secret name (<spoke>-home-worker-join).
const joinSecretSuffix = "home-worker-join"

// kubeconfigSecretSuffix is the CAPI-generated spoke kubeconfig Secret (<spoke>-kubeconfig).
const kubeconfigSecretSuffix = "kubeconfig"

// homeWorker namespace of the bootstrap token Secret on the spoke.
const kubeSystemNamespace = "kube-system"

// homeWorkerJoinEndpointAnnotation is the claim annotation carrying the Tailscale
// control-plane endpoint (host:port). When empty, the spoke kubeconfig server is
// used as the endpoint for the home workers.
const homeWorkerJoinEndpointAnnotation = "control-plane-endpoint-host"

// kubeadmBootstrapTokenCharset is the kubeadm token charset (lowercase alphanumeric).
const kubeadmBootstrapTokenCharset = "abcdefghijklmnopqrstuvwxyz0123456789"

// homeWorkerTokenPrefix is the Secret name prefix for spoke bootstrap tokens.
const homeWorkerTokenPrefix = "bootstrap-token-"

// homeWorkerJoinRequiringProvider is the provider value that activates the flow.
const homeWorkerJoinRequiringProvider = "hybrid"

// reconcileHomeWorkerJoin ensures the home-worker join payload Secret and spoke
// bootstrap token for a hybrid SpokePool. Gated on spec.provider == "hybrid" and
// the home-worker-enabled annotation. Idempotent and rotation-aware.
func (r *SpokePoolReconciler) reconcileHomeWorkerJoin(ctx context.Context, spokePool *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)
	spokeName := spokePool.GetName()

	// Gate: hybrid provider only.
	provider, found, err := unstructured.NestedString(spokePool.Object, "spec", "provider")
	if err != nil || !found {
		return nil
	}
	if provider != homeWorkerJoinRequiringProvider {
		return nil
	}

	// Gate: home-worker-enabled annotation must be "true".
	if spokePool.GetAnnotations()[homeWorkerEnabledAnnotation] != "true" {
		return nil
	}

	// Read claim annotations.
	ttl := spokePool.GetAnnotations()[bootstrapTokenTTLAnnotation]
	if ttl == "" {
		ttl = "24h"
	}
	ttlDuration, err := time.ParseDuration(ttl)
	if err != nil {
		return fmt.Errorf("parse home-worker-ttl %q: %w", ttl, err)
	}

	endpointHost := spokePool.GetAnnotations()[homeWorkerJoinEndpointAnnotation]

	homeWorkers := spokePool.GetAnnotations()[homeWorkersAnnotation]
	var workerList []struct {
		Hostname string `json:"hostname"`
	}
	if homeWorkers != "" {
		if err := json.Unmarshal([]byte(homeWorkers), &workerList); err != nil {
			logger.Info("home-workers annotation is not a valid JSON array — using 1 worker slot", "spoke", spokeName, "error", err)
		}
	}
	if len(workerList) == 0 {
		workerList = []struct {
			Hostname string `json:"hostname"`
		}{{Hostname: spokeName + "-home-worker"}}
	}

	// Read the spoke kubeconfig Secret (<spoke>-kubeconfig, platform-capi).
	// Uses the uncached client — the cache transform strips Secret .data
	// payloads, so the cached copy would have no 'value' key.
	kubeconfigSecret := &corev1.Secret{}
	if err := r.UncachedClient.Get(ctx, client.ObjectKey{
		Name:      spokeName + "-" + kubeconfigSecretSuffix,
		Namespace: "platform-capi",
	}, kubeconfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Spoke kubeconfig not ready yet — deferring home-worker join setup", "spoke", spokeName)
			return nil
		}
		return fmt.Errorf("read spoke kubeconfig Secret: %w", err)
	}

	kubeconfigBytes, ok := kubeconfigSecret.Data["value"]
	if !ok {
		return fmt.Errorf("spoke kubeconfig Secret %s has no 'value' key", kubeconfigSecret.Name)
	}

	// Connect to the spoke cluster to mint/rotate the bootstrap token.
	spokeClientset, caHash, spokeServer, err := buildSpokeClientset(ctx, kubeconfigBytes)
	if err != nil {
		return fmt.Errorf("connect to spoke %s: %w", spokeName, err)
	}

	// Resolve the control-plane endpoint for home workers: claim annotation
	// wins; fall back to the spoke API server address.
	controlPlaneEndpoint := endpointHost
	if controlPlaneEndpoint == "" {
		controlPlaneEndpoint = spokeServer
	}

	// Ensure the bootstrap token Secret exists on the spoke and is unexpired.
	tokenID, tokenSecret, err := r.ensureSpokeBootstrapToken(ctx, spokeClientset, spokeName, ttlDuration)
	if err != nil {
		return fmt.Errorf("ensure spoke bootstrap token: %w", err)
	}

	// Write the join payload Secret consumed by home-worker-join.sh.
	if err := r.writeJoinPayloadSecret(ctx, spokeName, workerList, tokenID, tokenSecret, caHash, controlPlaneEndpoint); err != nil {
		return fmt.Errorf("write home-worker join Secret: %w", err)
	}

	// Cleanup: remove expired tokens for this spoke (rotation hygiene).
	if err := r.pruneExpiredBootstrapTokens(ctx, spokeClientset, spokeName); err != nil {
		logger.Info("Failed to prune expired spoke bootstrap tokens", "spoke", spokeName, "error", err)
	}

	logger.Info("Home-worker join setup ensured", "spoke", spokeName, "endpoint", controlPlaneEndpoint)
	return nil
}

// buildSpokeClientset builds a Kubernetes clientset for the spoke from the
// CAPI-generated kubeconfig, and returns the discovery CA sha256 hash and the
// API server host:port.
func buildSpokeClientset(ctx context.Context, kubeconfig []byte) (*kubernetes.Clientset, string, string, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse spoke kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("build spoke clientset: %w", err)
	}

	caHash := computeDiscoveryTokenCAHash(cfg.CAData)
	server := cfg.Host

	// kubeadm join needs a bare host:port (no scheme/path). cfg.Host from
	// RESTConfigFromKubeConfig is a full URL like https://host:6443.
	if parsed, err := url.Parse(server); err == nil && parsed.Host != "" {
		server = parsed.Host
	}

	return clientset, caHash, server, nil
}

// computeDiscoveryTokenCAHash returns the kubeadm discovery-token-ca-cert-hash
// (sha256:<hex of the CA public key digest>). kubeadm pins the SubjectPublicKeyInfo
// (SPKI) of the cluster CA, NOT the certificate DER/PEM — hashing the raw cert
// bytes yields a mismatching pin and kubeadm rejects the join with
// "none of the public keys ... are pinned". See kubeadm pubkeypin.Hash.
func computeDiscoveryTokenCAHash(caData []byte) string {
	if len(caData) == 0 {
		return ""
	}
	block, _ := pem.Decode(caData)
	if block == nil {
		block = &pem.Block{Type: "CERTIFICATE", Bytes: caData}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	spki, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(spki)
	return hex.EncodeToString(sum[:])
}

// ensureSpokeBootstrapToken ensures a valid, unexpired bootstrap token Secret on
// the spoke in kube-system, rotating it before it enters the rotation window
// (TTL/2 remaining). Returns the kubeadm token as <id>.<secret>.
func (r *SpokePoolReconciler) ensureSpokeBootstrapToken(ctx context.Context, spoke *kubernetes.Clientset, spokeName string, ttl time.Duration) (string, string, error) {
	logger := log.FromContext(ctx)

	list, err := spoke.CoreV1().Secrets(kubeSystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "nutgraf.in/home-worker-spoke=" + spokeName,
	})
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	rotationWindow := ttl / 2

	// Reuse the newest valid token if it still has enough lifetime.
	for _, secret := range list.Items {
		if secret.Type != bootstrapTokenSecretType {
			continue
		}
		expRaw, ok := secret.Data["expiration"]
		if !ok {
			continue
		}
		expiration, err := time.Parse(time.RFC3339, string(expRaw))
		if err != nil {
			continue
		}
		if expiration.After(now.Add(rotationWindow)) {
			return string(secret.Data["token-id"]), string(secret.Data["token-secret"]), nil
		}
		logger.Info("Rotating expiring spoke bootstrap token", "spoke", spokeName, "secret", secret.Name, "expires", expiration.Format(time.RFC3339))
		if err := spoke.CoreV1().Secrets(kubeSystemNamespace).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil {
			logger.Info("Failed to delete expiring bootstrap token", "spoke", spokeName, "secret", secret.Name, "error", err)
		}
	}

	// Mint a fresh token.
	id, secret, err := newKubeadmBootstrapToken()
	if err != nil {
		return "", "", err
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      homeWorkerTokenPrefix + id,
			Namespace: kubeSystemNamespace,
			Labels: map[string]string{
				"nutgraf.in/home-worker-spoke": spokeName,
			},
		},
		Type: bootstrapTokenSecretType,
		Data: map[string][]byte{
			"token-id":                       []byte(id),
			"token-secret":                   []byte(secret),
			"expiration":                     []byte(time.Now().Add(ttl).UTC().Format(time.RFC3339)),
			"usage-bootstrap-authentication": []byte("true"),
			"usage-bootstrap-signing":        []byte("true"),
		},
	}

	if _, err := spoke.CoreV1().Secrets(kubeSystemNamespace).Create(ctx, tokenSecret, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return id, secret, nil
		}
		return "", "", err
	}

	logger.Info("Minted spoke bootstrap token", "spoke", spokeName, "token-id", id, "ttl", ttl.String())
	return id, secret, nil
}

// pruneExpiredBootstrapTokens deletes expired bootstrap tokens for the spoke.
func (r *SpokePoolReconciler) pruneExpiredBootstrapTokens(ctx context.Context, spoke *kubernetes.Clientset, spokeName string) error {
	list, err := spoke.CoreV1().Secrets(kubeSystemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "nutgraf.in/home-worker-spoke=" + spokeName,
	})
	if err != nil {
		return err
	}

	now := time.Now()
	for _, secret := range list.Items {
		if secret.Type != bootstrapTokenSecretType {
			continue
		}
		expRaw, ok := secret.Data["expiration"]
		if !ok {
			continue
		}
		expiration, err := time.Parse(time.RFC3339, string(expRaw))
		if err != nil || expiration.Before(now) {
			_ = spoke.CoreV1().Secrets(kubeSystemNamespace).Delete(ctx, secret.Name, metav1.DeleteOptions{})
		}
	}
	return nil
}

// writeJoinPayloadSecret writes the join payload Secret on the Hub
// (<spoke>-home-worker-join, platform-capi) consumed by home-worker-join.sh.
// Keys: node-<idx>-token (idx 1-based), ca-cert-hash, control-plane-endpoint.
func (r *SpokePoolReconciler) writeJoinPayloadSecret(ctx context.Context, spokeName string, workers []struct {
	Hostname string `json:"hostname"`
}, tokenID, tokenSecret, caHash, endpoint string) error {
	token := tokenID + "." + tokenSecret

	data := map[string][]byte{
		"ca-cert-hash":           []byte(caHash),
		"control-plane-endpoint": []byte(endpoint),
	}
	for i := range workers {
		data[fmt.Sprintf("node-%d-token", i+1)] = []byte(token)
	}

	joinSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spokeName + "-" + joinSecretSuffix,
			Namespace: "platform-capi",
			Labels: map[string]string{
				"nutgraf.in/home-worker": "true",
				"nutgraf.in/spoke":       spokeName,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: joinSecret.Name, Namespace: joinSecret.Namespace}, existing)
	if err == nil {
		existing.Data = data
		return r.Update(ctx, existing)
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	return r.Create(ctx, joinSecret)
}

// newKubeadmBootstrapToken generates a kubeadm token <id>.<secret> (6.16 chars).
func newKubeadmBootstrapToken() (id, secret string, err error) {
	id, err = randomTokenString(6)
	if err != nil {
		return "", "", err
	}
	secret, err = randomTokenString(16)
	if err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// randomTokenString returns n random characters from the kubeadm charset.
func randomTokenString(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	for i := range bytes {
		bytes[i] = kubeadmBootstrapTokenCharset[int(bytes[i])%len(kubeadmBootstrapTokenCharset)]
	}
	return string(bytes), nil
}

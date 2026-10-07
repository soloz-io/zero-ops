// Package nodecert issues the plugin's own client certificate, on the node, from the
// cluster's certificate authority.
//
// WHY THE PLUGIN HAS A CERTIFICATE AT ALL. ADR-100's bootstrap identity is X.509 Workload
// Identity Federation: the cluster's CA is the trust anchor of a per-cluster provider, and
// the plugin presents a certificate signed by that CA over mTLS to Google STS. The reason
// it is X.509 and not a projected ServiceAccount token is ordering — this process must
// answer before the API server can serve, so it cannot obtain a credential from the API
// server.
//
// WHY IT IS ISSUED HERE, IN THE initContainer, AND NOT BY A TEMPLATE COMMAND. The trap is
// specific and would cost a bootstrap:
//
//	the static pod manifest arrives as cloud-init `files:` content, BEFORE kubeadm runs
//	    -> kubelet starts and tries to run the plugin
//	        -> /etc/kubernetes/pki/ca.key does not exist yet; kubeadm creates it
//	            -> the plugin cannot authenticate, crash-loops
//	                -> the API server cannot start, because its encryption provider is down
//	                    -> kubeadm never completes, so the CA is never created
//
// A deadlock, and the symptom would be an API server that never comes up on a node that
// looks provisioned. `preKubeadmCommands` cannot fix it either: they run before kubeadm,
// which is before the PKI exists.
//
// An initContainer is the natural answer because it BLOCKS the pod. It waits for the CA,
// issues the certificate, and only then does the plugin container start. kubelet retries a
// failed initContainer, so "the CA is not there yet" resolves itself instead of
// deadlocking.
//
// WHY THE PLUGIN ITSELF DOES NOT DO THIS. Issuing requires the CA private key, and a
// process that can sign with the cluster CA can mint ANY identity in the cluster --
// including a kubelet or an API server client. The initContainer holds that capability for
// the seconds it needs; the long-running process that talks to the network does not, and
// it runs as 65532 with no access to ca.key at all.
//
// WHY IT RE-ISSUES ON EVERY START. kubeadm renews only its own hardcoded list under
// /etc/kubernetes/pki and will never touch this file. A certificate issued once at node
// bootstrap therefore expires silently about a year later, and the symptom is a control
// plane that cannot cold-read Secrets. Re-issuing whenever the pod starts means renewal
// happens through something that already occurs -- kubelet restarts static pods on node
// reboot and on every control-plane roll -- rather than through a timer somebody has to
// maintain.
package nodecert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Lifetime is how long an issued certificate is valid.
//
// THIRTY DAYS, which is a deliberate trade and not a default. It must comfortably exceed
// the longest expected uninterrupted pod lifetime, because nothing renews it except a pod
// start -- and it should be short enough that a certificate leaked off a node stops being
// useful quickly. A control plane that has not restarted in thirty days is unusual on a
// platform that rolls for every template change; if that stops being true, this number is
// wrong and `RemainingFraction` below is what will say so.
const Lifetime = 30 * 24 * time.Hour

// renewBelow re-issues when less than this fraction of the lifetime remains, so a pod
// that restarts frequently does not churn certificates and one that restarts rarely still
// gets a fresh one well before expiry.
const renewBelow = 0.5

// Paths names everything this reads and writes.
type Paths struct {
	// CACert and CAKey are kubeadm's cluster CA. The key is read and never copied.
	CACert string
	CAKey  string
	// Cert and Key are what the plugin presents. The credential configuration file
	// points at these.
	Cert string
	Key  string
}

// DefaultPaths matches the static pod's mounts.
func DefaultPaths() Paths {
	return Paths{
		CACert: "/etc/kubernetes/pki/ca.crt",
		CAKey:  "/etc/kubernetes/pki/ca.key",
		Cert:   "/etc/kubernetes/kms/tls.crt",
		Key:    "/etc/kubernetes/kms/tls.key",
	}
}

// CommonName is the certificate subject, and it is what the federation provider maps to a
// principal.
//
// PER CLUSTER, so the attribute mapping can distinguish one cluster's plugin from
// another's even though both chain to their own CA. The CA already separates them -- a
// certificate from cluster B does not validate against cluster A's provider -- so this is
// belt and braces rather than the control, and it makes an audit log readable.
func CommonName(cluster string) string {
	return "kms-plugin." + cluster
}

// Ensure issues a certificate if one is absent, expiring, or not ours.
//
// Returns whether it wrote one, so the caller can log the difference between a fresh node
// and a restart.
func Ensure(p Paths, cluster string) (bool, error) {
	if ok, err := usable(p, cluster); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}

	caPEM, err := os.ReadFile(p.CACert)
	if err != nil {
		return false, fmt.Errorf("reading the cluster CA certificate: %w\n\n"+
			"On a control-plane node kubeadm creates this. If it is absent the node is not "+
			"ready yet, and this container should be retried rather than skipped: starting "+
			"the plugin without a certificate produces an API server that cannot decrypt",
			err)
	}
	keyPEM, err := os.ReadFile(p.CAKey)
	if err != nil {
		return false, fmt.Errorf("reading the cluster CA private key: %w", err)
	}

	ca, caKey, err := parseCA(caPEM, keyPEM)
	if err != nil {
		return false, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generating a key for the plugin certificate: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: CommonName(cluster)},
		// Backdated a minute: a node whose clock is slightly behind the CA's would
		// otherwise present a certificate that is not yet valid, and the failure reads as
		// a trust problem rather than a clock one.
		NotBefore: now.Add(-time.Minute),
		NotAfter:  now.Add(Lifetime),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		// CLIENT auth only. This certificate authenticates TO Google; it never serves
		// anything, and a certificate that could do both is a certificate that can be
		// used for something nobody intended.
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return false, fmt.Errorf("signing the plugin certificate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(p.Cert), 0o700); err != nil {
		return false, err
	}
	// 0600 and written key-first: a certificate present without its key would let the
	// plugin start and fail to authenticate, which is a worse state than neither being
	// there, because `usable` would then have to distinguish them.
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return false, err
	}
	if err := writeFile(p.Key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return false, err
	}
	if err := writeFile(p.Cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// usable reports whether the certificate on disk can still be presented.
func usable(p Paths, cluster string) (bool, error) {
	certPEM, err := os.ReadFile(p.Cert)
	if err != nil {
		return false, nil // absent: issue one
	}
	if _, err := os.Stat(p.Key); err != nil {
		return false, nil // certificate without its key: re-issue both
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false, nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, nil
	}
	// Not ours: a certificate for a different cluster on this node means the node was
	// re-purposed, and presenting it would authenticate as the wrong principal -- or fail
	// against this cluster's provider, which is the better outcome but still wrong.
	if cert.Subject.CommonName != CommonName(cluster) {
		return false, nil
	}
	return RemainingFraction(cert, time.Now()) >= renewBelow, nil
}

// RemainingFraction is how much of the certificate's life is left, as 0..1.
//
// Exported because the plugin reports it: ADR-100 records that nothing yet surfaces the
// certificate's remaining lifetime, and a pod that happens not to restart expires into a
// failed cold read. A gauge makes that visible before it happens.
func RemainingFraction(cert *x509.Certificate, now time.Time) float64 {
	total := cert.NotAfter.Sub(cert.NotBefore)
	if total <= 0 {
		return 0
	}
	left := cert.NotAfter.Sub(now)
	if left <= 0 {
		return 0
	}
	return float64(left) / float64(total)
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, any, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, nil, fmt.Errorf("the cluster CA certificate is not PEM-encoded")
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the cluster CA certificate: %w", err)
	}
	if !ca.IsCA {
		return nil, nil, fmt.Errorf("%s is not a CA certificate, so nothing it signs would "+
			"chain to the trust anchor", ca.Subject.CommonName)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, nil, fmt.Errorf("the cluster CA private key is not PEM-encoded")
	}
	// kubeadm writes an RSA key; PKCS#8 is accepted too so this does not depend on which.
	if k, err := x509.ParsePKCS1PrivateKey(kb.Bytes); err == nil {
		return ca, k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the cluster CA private key: %w", err)
	}
	return ca, k, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// Explicit chmod: WriteFile's mode is masked by umask, and 0600 that became 0644
	// would publish the plugin's private key to every process on the node.
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("setting the mode of %s: %w", path, err)
	}
	return nil
}

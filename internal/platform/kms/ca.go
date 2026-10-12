package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// The dedicated per-cluster KMS certificate authority, and the plugin leaf it signs.
//
// WHY A DEDICATED CA AND NOT THE CLUSTER'S OWN. ADR-100 tried the cluster CA first, with
// the leaf issued on the node. That extends the cluster CA's authority into Cloud KMS: a
// GCP trust anchor holding that CA means Google trusts every certificate the cluster will
// ever issue, and only an IAM binding then separates a kubelet certificate from a KMS
// credential. One control deep, on the credential protecting every Secret.
//
// This CA signs exactly one kind of thing. A certificate from the cluster's PKI does not
// chain to it at all, so isolation is a property of the trust store rather than of a
// binding somebody could widen.
//
// WHERE THE KEYS LIVE, and the asymmetry is the whole design:
//
//	the CA private key   on the HUB, never on a spoke node, never in a template
//	the leaf private key on the node, as bootstrap material, scoped to one cluster's plugin
//
// A compromised node yields one credential. It cannot issue another, for itself or for
// anyone else, which is what A could not say.
//
// NOTHING HERE TOUCHES KUBERNETES OR GCP. It is pure crypto returning PEM, so the caller
// decides where material is stored and this stays testable without a cluster or a project.

// CAValidity is how long a dedicated KMS CA is good for.
//
// LONG, DELIBERATELY, because replacing it is not cheap: the CA is a GCP trust anchor, so
// rotating it means adding the new anchor, issuing new leaves, rolling every node of the
// cluster, verifying the new chain is in use, and only then removing the old anchor. That
// procedure has to exist for revocation regardless -- see ADR-100 -- but it is an outage
// shaped like a migration, not a routine renewal.
//
// Two years rather than ten: ten would outlive the design, and a credential nobody has
// ever replaced is a credential nobody knows how to replace.
const CAValidity = 2 * 365 * 24 * time.Hour

// LeafValidity is how long the plugin's certificate is good for.
//
// NINETY DAYS, matching the key's cryptoperiod, and renewed by a PLANNED control-plane
// roll rather than by a daemon. The alarm on
// soloz_kms_plugin_certificate_remaining_fraction is the backstop for the schedule
// failing, not the mechanism -- ADR-100 is explicit that an expiry cliff reached by
// chance of a roll is not an operational control.
//
// Short enough that a leaked leaf stops mattering on a known horizon; long enough that
// the roll is scheduled work rather than a treadmill.
const LeafValidity = 90 * 24 * time.Hour

// BootstrapLeafValidity is how long the bootstrap leaf credential is good for.
//
// ONE HOUR ONLY. Delivered to the node via cloud-init to bootstrap WIF authentication
// and unwrap the long-lived leaf on first boot. It is never renewed, so any leaked
// copy in user-data or hypervisor caches becomes cryptographically worthless within 1 hour.
const BootstrapLeafValidity = 1 * time.Hour

// renewLeafBelow is the fraction of LeafValidity at which a roll must already be
// scheduled. 0.3 of ninety days is twenty-seven days of margin, which is a planning
// horizon rather than an alert.
const renewLeafBelow = 0.3

// PEMPair is a certificate and its private key, both PEM-encoded.
//
// The key is a separate field rather than concatenated so a caller cannot accidentally
// store or log the pair as one blob -- which is how a private key ends up somewhere a
// certificate was expected.
type PEMPair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// CASubject names the authority, per cluster, so an audit log is readable.
func CASubject(cluster string) string { return "soloz-kms-ca." + cluster }

// LeafSubject is what the federation provider maps to a principal.
//
// The attribute mapping is google.subject from assertion.subject.dn.cn, so this string IS
// the identity. It carries the cluster because an audit entry naming only "kms-plugin"
// would not say whose.
func LeafSubject(cluster string) string { return "kms-plugin." + cluster }

// NewKMSCA creates a dedicated certificate authority for one cluster.
func NewKMSCA(cluster string) (*PEMPair, error) {
	if cluster == "" {
		return nil, fmt.Errorf("a KMS CA is per cluster and no cluster was named")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the CA key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: CASubject(cluster)},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(CAValidity),
		IsCA:         true,
		// MaxPathLenZero: this CA signs leaves and never another CA. An intermediate
		// under it would be a second issuer nobody decided to have.
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		BasicConstraintsValid: true,
		// KeyUsageCertSign only. No digital signature, no key encipherment: this
		// certificate's only job is signing one leaf, and anything else it could do is
		// something it should not.
		KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("self-signing the CA: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &PEMPair{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// IssueBootstrapLeaf signs the plugin's short-lived bootstrap client certificate with a
// cluster's KMS CA.
//
// Valid for BootstrapLeafValidity (1h). Delivered as bootstrap material via cloud-init to
// allow the plugin to authenticate to GCP and unwrap the long-lived leaf at first boot.
// It is never renewed.
func IssueBootstrapLeaf(ca *PEMPair, cluster string) (*PEMPair, error) {
	return issueLeaf(ca, cluster, BootstrapLeafValidity)
}

// IssueLeaf signs the plugin's client certificate with a cluster's KMS CA.
//
// ISSUED BY THE HUB, which is the point: the CA key is read here and nowhere near a spoke
// node. What reaches the node is the returned pair, and a node holding it can authenticate
// as one cluster's plugin and do nothing else.
func IssueLeaf(ca *PEMPair, cluster string) (*PEMPair, error) {
	return issueLeaf(ca, cluster, LeafValidity)
}

func issueLeaf(ca *PEMPair, cluster string, validity time.Duration) (*PEMPair, error) {
	caCert, caKey, err := parseKMSCA(ca)
	if err != nil {
		return nil, err
	}
	if want := CASubject(cluster); caCert.Subject.CommonName != want {
		// REFUSED RATHER THAN SIGNED. Issuing cluster A's leaf from cluster B's CA
		// produces a certificate that validates against B's trust anchor and claims A's
		// identity -- which breaks the per-cluster isolation this design exists for, and
		// would work until somebody audited it.
		return nil, fmt.Errorf("this CA is %q and a leaf for %q was requested: a leaf must be "+
			"signed by its own cluster's CA, or the certificate validates against the wrong "+
			"trust anchor", caCert.Subject.CommonName, want)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating the leaf key: %w", err)
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: LeafSubject(cluster)},
		// Backdated a minute: a node whose clock is slightly behind would otherwise
		// present a certificate that is not yet valid, and that failure reads as a trust
		// problem rather than a clock one.
		NotBefore: now.Add(-time.Minute),
		NotAfter:  now.Add(validity),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		// CLIENT auth only, and no SANs. This certificate authenticates TO Google STS and
		// never serves anything; a certificate that could do both is one that gets used
		// for something nobody intended.
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("signing the plugin leaf: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &PEMPair{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// MarshalLeaf serializes a leaf PEMPair into JSON format for KMS wrapping.
func MarshalLeaf(leaf *PEMPair) ([]byte, error) {
	if leaf == nil || len(leaf.CertPEM) == 0 || len(leaf.KeyPEM) == 0 {
		return nil, fmt.Errorf("a leaf PEMPair must contain both certificate and private key")
	}
	return json.Marshal(map[string]string{
		"cert": string(leaf.CertPEM),
		"key":  string(leaf.KeyPEM),
	})
}

// UnmarshalLeaf deserializes a decrypted leaf payload (JSON or concatenated PEM) into a PEMPair.
func UnmarshalLeaf(raw []byte) (*PEMPair, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty leaf payload")
	}
	var m struct {
		Cert    string `json:"cert"`
		Key     string `json:"key"`
		CertPEM []byte `json:"cert_pem"`
		KeyPEM  []byte `json:"key_pem"`
	}
	if err := json.Unmarshal(raw, &m); err == nil {
		cert := []byte(m.Cert)
		if len(cert) == 0 {
			cert = m.CertPEM
		}
		key := []byte(m.Key)
		if len(key) == 0 {
			key = m.KeyPEM
		}
		if len(cert) > 0 && len(key) > 0 {
			return &PEMPair{CertPEM: cert, KeyPEM: key}, nil
		}
	}
	// Fallback: parse PEM blocks
	var certPEM, keyPEM []byte
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certPEM = pem.EncodeToMemory(block)
		} else if strings.Contains(block.Type, "PRIVATE KEY") {
			keyPEM = pem.EncodeToMemory(block)
		}
	}
	if len(certPEM) > 0 && len(keyPEM) > 0 {
		return &PEMPair{CertPEM: certPEM, KeyPEM: keyPEM}, nil
	}
	return nil, fmt.Errorf("could not decode leaf payload as JSON or PEM blocks")
}

// KeyEncrypterDecrypter captures the Cloud KMS Encrypt and Decrypt operations.
// Satisfied by *Client.
type KeyEncrypterDecrypter interface {
	Encrypt(ctx context.Context, target string, plaintext []byte) (name string, ciphertext string, err error)
	Decrypt(ctx context.Context, keyName, ciphertext string) ([]byte, error)
}

// WrapLeaf wraps a long-lived leaf PEMPair under a Cloud KMS key.
func WrapLeaf(ctx context.Context, client KeyEncrypterDecrypter, keyName string, leaf *PEMPair) (string, error) {
	if client == nil {
		return "", fmt.Errorf("no KMS client provided")
	}
	payload, err := MarshalLeaf(leaf)
	if err != nil {
		return "", fmt.Errorf("marshaling leaf for wrapping: %w", err)
	}
	_, ciphertext, err := client.Encrypt(ctx, keyName, payload)
	if err != nil {
		return "", fmt.Errorf("wrapping leaf under %s: %w", keyName, err)
	}
	return ciphertext, nil
}

// UnwrapLeaf unwraps a wrapped leaf ciphertext string under a Cloud KMS key.
func UnwrapLeaf(ctx context.Context, client KeyEncrypterDecrypter, keyName string, ciphertext string) (*PEMPair, error) {
	if client == nil {
		return nil, fmt.Errorf("no KMS client provided")
	}
	if ciphertext == "" {
		return nil, fmt.Errorf("ciphertext is empty")
	}
	plaintext, err := client.Decrypt(ctx, keyName, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("unwrapping leaf under %s: %w", keyName, err)
	}
	return UnmarshalLeaf(plaintext)
}

// CASecretName returns the Secret name holding the dedicated KMS CA for a cluster.
func CASecretName(cluster string) string {
	return assets.KMSCASecretName(cluster)
}

// BootstrapSecretName returns the Secret name holding the short-lived bootstrap leaf.
func BootstrapSecretName(cluster string) string {
	return assets.KMSPluginBootstrapSecretName(cluster)
}

// WrappedSecretName returns the Secret name holding the long-lived KMS-wrapped leaf.
func WrappedSecretName(cluster string) string {
	return assets.KMSPluginWrappedSecretName(cluster)
}

// LeafNeedsRenewal reports whether a roll should already be scheduled.
//
// A SCHEDULING QUESTION, NOT AN ALARM. It returns true with weeks of margin so the roll is
// planned work; the metric and its alert exist for when the planning did not happen.
func LeafNeedsRenewal(certPEM []byte, now time.Time) (bool, time.Duration, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return true, 0, fmt.Errorf("not PEM-encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true, 0, fmt.Errorf("not a certificate: %w", err)
	}
	left := cert.NotAfter.Sub(now)
	total := cert.NotAfter.Sub(cert.NotBefore)
	if total <= 0 {
		return true, 0, nil
	}
	return float64(left)/float64(total) < renewLeafBelow, left, nil
}

func parseKMSCA(ca *PEMPair) (*x509.Certificate, any, error) {
	if ca == nil {
		return nil, nil, fmt.Errorf("no CA was supplied")
	}
	cb, _ := pem.Decode(ca.CertPEM)
	if cb == nil {
		return nil, nil, fmt.Errorf("the CA certificate is not PEM-encoded")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, nil, fmt.Errorf("%q is not a CA certificate, so nothing it signed would "+
			"chain to the trust anchor", cert.Subject.CommonName)
	}
	kb, _ := pem.Decode(ca.KeyPEM)
	if kb == nil {
		return nil, nil, fmt.Errorf("the CA private key is not PEM-encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing the CA private key: %w", err)
	}
	return cert, key, nil
}

func serialNumber() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating a serial number: %w", err)
	}
	return n, nil
}

package credential

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
)

// SetupOptions configures the two-phase credential startup sequence (ADR-100).
type SetupOptions struct {
	CertPath            string
	KeyPath             string
	WrappedLeafPath     string
	SteadyStateCredFile string
	BootstrapCredFile   string

	// Now can be overridden in tests. Defaults to time.Now.
	Now func() time.Time

	// StoreBuilder constructs a keystore.Store. If nil, defaults to keystore.FromEnv.
	StoreBuilder func() (keystore.Store, error)
}

// LeafPair holds the PEM-encoded certificate and private key of a leaf.
type LeafPair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// Setup executes the two-phase credential startup sequence in ADR-100:
//
//  1. tls.crt/tls.key present and valid?  → use them. Every boot after the first.
//  2. else leaf.enc present?              → construct a client from the BOOTSTRAP credential
//     config, Unwrap leaf.enc, write tls.crt/tls.key
//     (0600), then construct the steady-state
//     client and continue.
//  3. else                                → REFUSE TO START, naming which file was missing.
//
// CONSTRAINT: There must be no path from step 1 back to step 2. If tls.* exists but is
// invalid or expired, that is a hard failure, not a reason to re-unwrap.
func Setup(ctx context.Context, opts SetupOptions) error {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	storeBuilder := opts.StoreBuilder
	if storeBuilder == nil {
		storeBuilder = keystore.FromEnv
	}

	certExists := fileExists(opts.CertPath)
	keyExists := fileExists(opts.KeyPath)
	leafEncExists := fileExists(opts.WrappedLeafPath)

	// Step 1: tls.crt/tls.key present and valid?
	// If either file exists on disk, we are on the steady-state reboot path.
	// Any validation error or expiry here MUST hard-fail: we must never fall back to step 2.
	if certExists || keyExists {
		if !certExists {
			return fmt.Errorf("refusing to start: private key %s exists but certificate %s is missing; will not fall back to bootstrap credential",
				opts.KeyPath, opts.CertPath)
		}
		if !keyExists {
			return fmt.Errorf("refusing to start: certificate %s exists but private key %s is missing; will not fall back to bootstrap credential",
				opts.CertPath, opts.KeyPath)
		}

		cert, err := ValidateCertificateFile(opts.CertPath, opts.KeyPath)
		if err != nil {
			return fmt.Errorf("refusing to start: existing tls credential (%s, %s) is invalid: %w; will not fall back to bootstrap credential",
				opts.CertPath, opts.KeyPath, err)
		}

		now := nowFn()
		if now.After(cert.NotAfter) {
			return fmt.Errorf("refusing to start: tls certificate %s is expired (expired at %v, current time %v); will not fall back to bootstrap credential",
				opts.CertPath, cert.NotAfter, now)
		}
		if now.Before(cert.NotBefore) {
			return fmt.Errorf("refusing to start: tls certificate %s is not yet valid (valid from %v, current time %v); will not fall back to bootstrap credential",
				opts.CertPath, cert.NotBefore, now)
		}

		log.Printf("[kms-plugin] steady-state credential valid (certificate %s expires in %v); proceeding to steady-state",
			opts.CertPath, cert.NotAfter.Sub(now))

		if opts.SteadyStateCredFile != "" {
			if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", opts.SteadyStateCredFile); err != nil {
				return fmt.Errorf("setting GOOGLE_APPLICATION_CREDENTIALS to %s: %w", opts.SteadyStateCredFile, err)
			}
		}
		return nil
	}

	// Step 2: else leaf.enc present?
	if leafEncExists {
		log.Printf("[kms-plugin] first boot: %s found, unwrapping long-lived leaf using bootstrap credential...", opts.WrappedLeafPath)

		if opts.BootstrapCredFile != "" {
			if !fileExists(opts.BootstrapCredFile) {
				return fmt.Errorf("refusing to start: bootstrap credential config %s missing while %s exists",
					opts.BootstrapCredFile, opts.WrappedLeafPath)
			}
			if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", opts.BootstrapCredFile); err != nil {
				return fmt.Errorf("setting GOOGLE_APPLICATION_CREDENTIALS to %s: %w", opts.BootstrapCredFile, err)
			}
		} else if os.Getenv("KMS_INSECURE_TEST_ENDPOINT") == "" {
			return fmt.Errorf("refusing to start: missing bootstrap credential configuration (--google-bootstrap-credentials) required to unwrap %s",
				opts.WrappedLeafPath)
		}

		bootstrapStore, err := storeBuilder()
		if err != nil {
			return fmt.Errorf("constructing bootstrap KMS client to unwrap %s: %w", opts.WrappedLeafPath, err)
		}

		raw, err := os.ReadFile(opts.WrappedLeafPath)
		if err != nil {
			return fmt.Errorf("reading wrapped leaf %s: %w", opts.WrappedLeafPath, err)
		}
		if len(raw) == 0 {
			return fmt.Errorf("wrapped leaf %s is empty", opts.WrappedLeafPath)
		}

		plaintext, err := UnwrapLeafBlob(ctx, bootstrapStore, raw)
		if err != nil {
			return fmt.Errorf("unwrapping %s via Cloud KMS: %w", opts.WrappedLeafPath, err)
		}

		pair, err := ParseLeafPayload(plaintext)
		if err != nil {
			return fmt.Errorf("parsing unwrapped leaf payload from %s: %w", opts.WrappedLeafPath, err)
		}

		cert, err := ValidateCertificateAndKeyBytes(pair.CertPEM, pair.KeyPEM)
		if err != nil {
			return fmt.Errorf("validating unwrapped leaf certificate: %w", err)
		}

		now := nowFn()
		if now.After(cert.NotAfter) {
			return fmt.Errorf("unwrapped leaf certificate is already expired (expired at %v, current time %v)", cert.NotAfter, now)
		}
		if now.Before(cert.NotBefore) {
			return fmt.Errorf("unwrapped leaf certificate is not yet valid (valid from %v, current time %v)", cert.NotBefore, now)
		}

		if err := os.MkdirAll(filepath.Dir(opts.CertPath), 0o700); err != nil {
			return fmt.Errorf("creating directory for certificate %s: %w", opts.CertPath, err)
		}
		if err := os.WriteFile(opts.CertPath, pair.CertPEM, 0o600); err != nil {
			return fmt.Errorf("writing certificate %s: %w", opts.CertPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(opts.KeyPath), 0o700); err != nil {
			return fmt.Errorf("creating directory for key %s: %w", opts.KeyPath, err)
		}
		if err := os.WriteFile(opts.KeyPath, pair.KeyPEM, 0o600); err != nil {
			return fmt.Errorf("writing private key %s: %w", opts.KeyPath, err)
		}

		log.Printf("[kms-plugin] successfully unwrapped %s and wrote %s and %s (0600, valid until %v)",
			opts.WrappedLeafPath, opts.CertPath, opts.KeyPath, cert.NotAfter)

		if opts.SteadyStateCredFile != "" {
			if err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", opts.SteadyStateCredFile); err != nil {
				return fmt.Errorf("switching GOOGLE_APPLICATION_CREDENTIALS to steady-state config: %w", err)
			}
		}
		return nil
	}

	// Step 3: Neither exists -> refuse to start, naming which file was missing.
	return fmt.Errorf("refusing to start: neither steady-state certificate (%s) nor wrapped leaf (%s) found",
		opts.CertPath, opts.WrappedLeafPath)
}

// UnwrapLeafBlob unwraps a wrapped leaf ciphertext using the provided keystore.
//
// Invariant: The wrapped leaf file MUST be standard base64-encoded text, as produced
// by Cloud KMS Encrypt and templated into Secret stringData (kms-plugin-wrapped.yaml).
// Raw binary ciphertext is strictly rejected with an informative error.
func UnwrapLeafBlob(ctx context.Context, store keystore.Store, raw []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("wrapped leaf blob is empty; expected base64-encoded ciphertext")
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("wrapped leaf blob is not valid standard base64 (%w); expected base64-encoded ciphertext, but found %s",
			err, formatByteShape(raw))
	}

	plaintext, err := store.Unwrap(ctx, decoded)
	if err != nil {
		return nil, fmt.Errorf("unwrapping leaf ciphertext via Cloud KMS: %w", err)
	}
	return plaintext, nil
}

// ParseLeafPayload decodes an unwrapped leaf payload into certificate and key PEM bytes.
//
// Invariant: Accepts ONLY a JSON object with exactly two keys: {"cert": "...", "key": "..."}.
// Matches MarshalLeaf (internal/platform/kms/ca.go:220). Any legacy aliases (cert_pem/key_pem),
// concatenated PEM blocks, or extraneous keys are strictly rejected with an informative error.
func ParseLeafPayload(raw []byte) (*LeafPair, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty leaf payload; expected JSON object with keys %q and %q", "cert", "key")
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("invalid leaf payload: payload is not a valid JSON object (%v); expected JSON object with keys %q and %q, but found %s",
			err, "cert", "key", formatByteShape(raw))
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) != 2 || m["cert"] == nil || m["key"] == nil {
		return nil, fmt.Errorf("invalid leaf payload: found JSON keys [%s]; expected exactly keys %q and %q",
			strings.Join(keys, ", "), "cert", "key")
	}

	var certStr, keyStr string
	if err := json.Unmarshal(m["cert"], &certStr); err != nil {
		return nil, fmt.Errorf("invalid leaf payload: %q value is not a JSON string (%w); expected PEM string", "cert", err)
	}
	if err := json.Unmarshal(m["key"], &keyStr); err != nil {
		return nil, fmt.Errorf("invalid leaf payload: %q value is not a JSON string (%w); expected PEM string", "key", err)
	}

	if strings.TrimSpace(certStr) == "" {
		return nil, fmt.Errorf("invalid leaf payload: %q field is empty; expected non-empty PEM string", "cert")
	}
	if strings.TrimSpace(keyStr) == "" {
		return nil, fmt.Errorf("invalid leaf payload: %q field is empty; expected non-empty PEM string", "key")
	}

	return &LeafPair{
		CertPEM: []byte(certStr),
		KeyPEM:  []byte(keyStr),
	}, nil
}

func formatByteShape(data []byte) string {
	if len(data) == 0 {
		return "empty data"
	}
	sampleLen := 32
	if len(data) < sampleLen {
		sampleLen = len(data)
	}
	sample := data[:sampleLen]

	isPrintable := true
	for _, b := range sample {
		if (b < 0x20 || b > 0x7e) && b != '\n' && b != '\r' && b != '\t' {
			isPrintable = false
			break
		}
	}
	if isPrintable {
		return fmt.Sprintf("ASCII/text %q (total %d bytes)", string(sample), len(data))
	}
	return fmt.Sprintf("raw binary 0x%x (total %d bytes)", sample, len(data))
}

// ValidateCertificateFile reads and validates the certificate and private key from files.
func ValidateCertificateFile(certPath, keyPath string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("reading certificate %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key %s: %w", keyPath, err)
	}
	return ValidateCertificateAndKeyBytes(certPEM, keyPEM)
}

// ValidateCertificateAndKeyBytes validates that the certificate and private key parse and match.
func ValidateCertificateAndKeyBytes(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("certificate PEM block missing")
	}
	if certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("unexpected PEM type %q, expected CERTIFICATE", certBlock.Type)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("private key PEM block missing")
	}
	privKey, err := parsePrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	if err := matchKey(cert.PublicKey, privKey); err != nil {
		return nil, fmt.Errorf("certificate public key does not match private key: %w", err)
	}
	return cert, nil
}

func parsePrivateKey(der []byte) (any, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	return nil, errors.New("unrecognized private key format")
}

func matchKey(pub any, priv any) error {
	switch p := priv.(type) {
	case *ecdsa.PrivateKey:
		pubECDSA, ok := pub.(*ecdsa.PublicKey)
		if !ok || !p.PublicKey.Equal(pubECDSA) {
			return errors.New("ECDSA public key does not match private key")
		}
	case *rsa.PrivateKey:
		pubRSA, ok := pub.(*rsa.PublicKey)
		if !ok || !p.PublicKey.Equal(pubRSA) {
			return errors.New("RSA public key does not match private key")
		}
	case ed25519.PrivateKey:
		pubEd, ok := pub.(ed25519.PublicKey)
		if !ok || !bytes.Equal(p.Public().(ed25519.PublicKey), pubEd) {
			return errors.New("Ed25519 public key does not match private key")
		}
	default:
		return fmt.Errorf("unsupported private key type %T", priv)
	}
	return nil
}

// PrepareCredentialDir ensures the credential directory exists, its files are owned
// by uid, and the directory is handed over to uid mode 0700.
//
// Ordering is critical (W14):
// 1. If already 0700 owned by uid, return nil (restart path performs no privileged syscalls).
// 2. MkdirAll(dir, 0700)
// 3. Chown(dir, 0, 0) - become the owner
// 4. Chmod(dir, 0700) - restrict permissions
// 5. ReadDir and Chown each file to uid WHILE root still owns the directory (CAP_CHOWN without CAP_DAC_OVERRIDE)
// 6. Chown(dir, uid, uid) - hand over the directory last
func PrepareCredentialDir(dir string, uid int) error {
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			if int(st.Uid) == uid && int(st.Gid) == uid && fi.Mode().Perm() == 0o700 {
				return nil
			}
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating credential directory %s: %w", dir, err)
	}
	// Step 2: become the owner, so step 3 does not need CAP_FOWNER.
	if err := os.Chown(dir, 0, 0); err != nil {
		return fmt.Errorf("taking ownership of %s to set mode (needs CAP_CHOWN): %w", dir, err)
	}
	// Step 3: an owner chmod.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restricting %s to 0700: %w", dir, err)
	}
	// Step 4: ensure existing files are readable by uid BEFORE handing over the directory.
	// Done while root still owns dir so ReadDir does not need CAP_DAC_OVERRIDE.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading credential directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		if err := os.Chown(entryPath, uid, uid); err != nil {
			return fmt.Errorf("chowning %s to uid %d: %w", entryPath, uid, err)
		}
	}
	// Step 5: hand the directory over to the plugin's uid last.
	if err := os.Chown(dir, uid, uid); err != nil {
		return fmt.Errorf("giving %s to uid %d (needs CAP_CHOWN): %w", dir, uid, err)
	}
	return nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

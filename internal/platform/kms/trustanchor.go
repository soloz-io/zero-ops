package kms

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// The trust anchor: the half of the bootstrap identity that a rebuild silently breaks.
//
// WHAT GOES WRONG WITHOUT THIS. A rebuilt cluster gets a NEW certificate authority. The
// workload identity pool provider still holds the OLD one as its trust anchor, so every
// certificate the new control plane presents fails validation, so the plugin cannot
// authenticate, so the API server cannot decrypt a single Secret. The cluster comes up
// and is useless, and the first symptom is three layers from the cause -- a KMS provider
// reporting unhealthy, which reads as a plugin bug rather than as a step somebody skipped.
//
// ADR-100 chose "re-upload the trust anchor during a rebuild" over escrowing the cluster
// CA, because it adds a procedure rather than a standing exposure. A procedure that fails
// silently when skipped is not good enough for a platform whose whole claim is that
// operations are not manual, so this is the enforcement: the check that lets a caller
// refuse to configure KMS encryption BEFORE the API server is made to depend on it.
//
// It reads and changes nothing.

// trustMismatch marks a CONFIRMED refusal: the provider was read and does not trust this
// CA. It exists so a caller can tell that apart from "the provider could not be read",
// without matching on message text.
type trustMismatch struct{ error }

// IsTrustMismatch reports whether err is a confirmed trust-anchor mismatch rather than a
// failure to determine one.
func IsTrustMismatch(err error) bool {
	var m trustMismatch
	return errors.As(err, &m)
}

// workloadIdentityProvider is the subset of the IAM resource this needs.
type workloadIdentityProvider struct {
	Name     string `json:"name"`
	Disabled bool   `json:"disabled"`
	X509     *struct {
		TrustStore *struct {
			TrustAnchors []struct {
				PemCertificate string `json:"pemCertificate"`
			} `json:"trustAnchors"`
			IntermediateCas []struct {
				PemCertificate string `json:"pemCertificate"`
			} `json:"intermediateCas"`
		} `json:"trustStore"`
	} `json:"x509"`
}

// ProviderName builds the IAM resource name for one cluster's federation provider.
//
// One pool, one provider per cluster: that is what makes identity isolation structural
// rather than configured. A certificate from cluster B does not validate against cluster
// A's provider because the two carry different CAs.
func (c *Client) ProviderName(pool, provider string) string {
	return fmt.Sprintf("projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
		c.projectID, pool, provider)
}

// fingerprint is the SHA-256 of a certificate's DER bytes.
//
// COMPARED BY FINGERPRINT, NOT BY STRING. The same certificate round-tripped through
// different tools differs in line wrapping, trailing newlines and header spacing, and a
// string comparison would report a mismatch that is not one -- which is the kind of false
// alarm that gets a check disabled. Two certificates with the same DER are the same
// certificate.
func fingerprint(pemBytes []byte) (string, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", fmt.Errorf("not PEM-encoded")
	}
	if block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("PEM block is %q, want CERTIFICATE", block.Type)
	}
	// Parsed rather than hashed raw: a block that is not a certificate at all would
	// otherwise produce a confident fingerprint of something else.
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("not a certificate: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// TrustAnchors returns the fingerprints the provider currently trusts.
func (c *Client) TrustAnchors(ctx context.Context, pool, provider string) ([]string, error) {
	var out workloadIdentityProvider
	url := c.iamHost + "/v1/" + c.ProviderName(pool, provider)
	if err := c.call(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, err
	}
	if out.X509 == nil || out.X509.TrustStore == nil {
		return nil, fmt.Errorf("%s declares no x509 trust store. ADR-100's bootstrap identity "+
			"is X.509 federation; a provider configured for OIDC or AWS cannot validate a "+
			"control-plane certificate", c.ProviderName(pool, provider))
	}
	var fps []string
	for _, a := range out.X509.TrustStore.TrustAnchors {
		fp, err := fingerprint([]byte(a.PemCertificate))
		if err != nil {
			return nil, fmt.Errorf("a trust anchor on %s is unreadable: %w",
				c.ProviderName(pool, provider), err)
		}
		fps = append(fps, fp)
	}
	if out.Disabled {
		return fps, fmt.Errorf("%s is DISABLED. Its trust anchors are irrelevant while it is: "+
			"no certificate validates against a disabled provider",
			c.ProviderName(pool, provider))
	}
	return fps, nil
}

// VerifyTrustAnchor refuses unless the provider trusts exactly this CA.
//
// THE REFUSAL IS THE POINT. A caller uses it to decline configuring KMS encryption, so
// the failure lands where somebody is running a command rather than hours later as a
// control plane that will not serve.
func (c *Client) VerifyTrustAnchor(ctx context.Context, pool, provider string, caPEM []byte) error {
	want, err := fingerprint(caPEM)
	if err != nil {
		return fmt.Errorf("the cluster CA supplied for comparison is unusable: %w", err)
	}

	have, err := c.TrustAnchors(ctx, pool, provider)
	if err != nil {
		if NotFound(err) {
			return fmt.Errorf("%s does not exist, so nothing can authenticate against it.\n\n"+
				"This is the rebuild trap: a new cluster has a new certificate authority, and "+
				"until its CA is the provider's trust anchor the plugin cannot authenticate, "+
				"the API server cannot decrypt, and the cluster comes up unable to read a "+
				"single Secret. Establish the provider for this cluster before configuring "+
				"KMS encryption.", c.ProviderName(pool, provider))
		}
		return err
	}

	for _, h := range have {
		if h == want {
			return nil
		}
	}
	return trustMismatch{fmt.Errorf("%s does not trust this cluster's certificate authority.\n\n"+
		"  cluster CA      sha256:%s\n"+
		"  trusted anchors %s\n\n"+
		"A certificate signed by this CA will fail validation, so the plugin cannot "+
		"authenticate and the API server cannot decrypt. If this is a rebuild, the provider "+
		"still holds the PREVIOUS cluster's CA and the trust anchor has to be replaced "+
		"before the control plane is configured to depend on it.",
		c.ProviderName(pool, provider), want, describeAnchors(have))}
}

func describeAnchors(fps []string) string {
	if len(fps) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, f := range fps {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("sha256:" + f)
	}
	return b.String()
}

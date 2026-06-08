package infisical

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const fleetCAName = "Fleet Intermediate CA"

type caEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type policyEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// profileDefinition defines a certificate profile to create during Day-0 bootstrap.
// Per ADR-042 (PKI_READY state), profiles for all standard TTL tiers must exist.
// TTLs are server-side caps; cert-manager Certificate duration controls actual rotation.
type profileDefinition struct {
	Slug    string
	TTLDays int
}

// requiredProfiles lists ALL certificate profiles mandated by ADR-042:70 plus
// the signing-keys profile required for the argocd-agent-jwt Certificate CR
// (Correction 1 from ADR-035 PKI review).
var requiredProfiles = []profileDefinition{
	{Slug: "argocd-bootstrap", TTLDays: 3},      // 72h bootstrap exception (ADR-035)
	{Slug: "infrastructure-services", TTLDays: 1}, // 24h cap (ADR-042)
	{Slug: "database-clients", TTLDays: 1},         // 4h cap, rounded up (ADR-042)
	{Slug: "service-mesh", TTLDays: 1},             // 1h cap, rounded up (ADR-042)
	{Slug: "human-access", TTLDays: 1},             // 15m cap, rounded up (ADR-042)
	{Slug: "signing-keys", TTLDays: 3650},          // 10yr JWT signing keys
}

// ensureCertificateProfiles creates all required certificate profiles if they do not exist.
// It looks up the Fleet Intermediate CA, finds or creates a certificate policy, then creates
// each profile. This replaces the manual Step 3.6 UI action from the old workflow and
// satisfies ADR-042 PKI_READY exit criteria.
func ensureCertificateProfiles(ctx context.Context, podName, adminJWT, projectID string) error {
	// Phase 1: Ensure Fleet Intermediate CA exists and is active (once for all profiles)
	cas, err := listCertificateAuthorities(ctx, podName, adminJWT, projectID)
	if err != nil {
		return fmt.Errorf("list certificate authorities: %w", err)
	}

	var fleetCAID string
	for _, ca := range cas {
		if ca.Name == fleetCAName {
			fleetCAID = ca.ID
			break
		}
	}
	if fleetCAID == "" {
		fleetCAID, err = createFleetIntermediateCA(ctx, podName, adminJWT, projectID)
		if err != nil {
			return fmt.Errorf("create CA %q: %w", fleetCAName, err)
		}
		fmt.Printf("[infisical-bootstrap] Created CA %q: id=%s\n", fleetCAName, fleetCAID)

		if err := activateRootCA(ctx, podName, adminJWT, fleetCAID); err != nil {
			return fmt.Errorf("activate CA %q: %w", fleetCAName, err)
		}
		fmt.Printf("[infisical-bootstrap] Activated CA %q: id=%s\n", fleetCAName, fleetCAID)
	} else {
		fmt.Printf("[infisical-bootstrap] Found CA %q: id=%s\n", fleetCAName, fleetCAID)
	}

	// Phase 2: Resolve certificate policy (one policy covers all profiles)
	policyID, err := resolveCertificatePolicy(ctx, podName, adminJWT, projectID)
	if err != nil {
		return fmt.Errorf("resolve certificate policy: %w", err)
	}

	// Phase 3: Create each required profile idempotently
	for _, p := range requiredProfiles {
		found, err := CheckCertificateProfile(ctx, podName, adminJWT, projectID, p.Slug)
		if err != nil {
			return fmt.Errorf("check profile %q: %w", p.Slug, err)
		}
		if found {
			fmt.Printf("[infisical-bootstrap] ✓ Certificate profile %q already exists\n", p.Slug)
			continue
		}

		if err := createCertProfile(ctx, podName, adminJWT, projectID, fleetCAID, policyID, p.Slug, p.TTLDays); err != nil {
			return fmt.Errorf("create certificate profile %q: %w", p.Slug, err)
		}
		fmt.Printf("[infisical-bootstrap] ✓ Certificate profile %q created (TTL: %d days)\n", p.Slug, p.TTLDays)
	}
	return nil
}

// listCertificateAuthorities returns all CAs in the given project.
func listCertificateAuthorities(ctx context.Context, podName, adminJWT, projectID string) ([]caEntry, error) {
	url := fmt.Sprintf("http://localhost:%s%s?projectId=%s", infisicalPort, PathCertificateAuthorities, projectID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s",
		url,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return nil, fmt.Errorf("curl failed: %w", err)
	}

	var resp struct {
		CertificateAuthorities []caEntry `json:"certificateAuthorities"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("parse CA list: %w\nresponse: %s", err, output)
	}
	return resp.CertificateAuthorities, nil
}

// listCertificatePolicies returns all certificate policies in the given project.
func listCertificatePolicies(ctx context.Context, podName, adminJWT, projectID string) ([]policyEntry, error) {
	url := fmt.Sprintf("http://localhost:%s%s?projectId=%s", infisicalPort, PathCertificatePolicies, projectID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s",
		url,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return nil, fmt.Errorf("curl failed: %w", err)
	}

	var resp struct {
		CertificatePolicies []policyEntry `json:"certificatePolicies"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("parse policy list: %w\nresponse: %s", err, output)
	}
	return resp.CertificatePolicies, nil
}

// createCertificatePolicy creates a new certificate policy with the given name and returns its ID.
// The subject policy allows argocd-agent.* common names — the cert-operator prefixes
// spoke names with "argocd-agent." when issuing bootstrap certificates.
func createCertificatePolicy(ctx context.Context, podName, adminJWT, projectID, name string) (string, error) {
	body := fmt.Sprintf(`{"projectId":"%s","name":"%s","subject":[{"type":"common_name","allowed":["argocd-agent.*"]}]}`, projectID, name)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+PathCertificatePolicies,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return "", fmt.Errorf("curl failed: %w", err)
	}

	var resp struct {
		CertificatePolicy policyEntry `json:"certificatePolicy"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return "", fmt.Errorf("parse create policy response: %w\nresponse: %s", err, output)
	}
	if resp.CertificatePolicy.ID == "" {
		return "", fmt.Errorf("create policy returned empty ID: %s", output)
	}
	return resp.CertificatePolicy.ID, nil
}

func createCertProfile(ctx context.Context, podName, adminJWT, projectID, caID, policyID, slug string, ttlDays int) error {
	body := fmt.Sprintf(
		`{"projectId":"%s","caId":"%s","certificatePolicyId":"%s","slug":"%s","enrollmentType":"api","issuerType":"ca","apiConfig":{"autoRenew":false},"externalConfigs":null,"defaults":{"ttlDays":%d}}`,
		projectID, caID, policyID, slug, ttlDays,
	)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+PathCertificateProfiles,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return fmt.Errorf("curl failed: %w", err)
	}

	if strings.Contains(output, "already exists") || strings.Contains(output, "Conflict") {
		fmt.Printf("[infisical-bootstrap] Certificate profile %q already exists (race)\n", slug)
		return nil
	}

	var resp struct {
		CertificateProfile struct {
			ID string `json:"id"`
		} `json:"certificateProfile"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return fmt.Errorf("parse create profile response: %w\nresponse: %s", err, output)
	}
	if resp.CertificateProfile.ID == "" {
		return fmt.Errorf("create profile returned empty ID: %s", output)
	}
	return nil
}

// resolveCertificatePolicy finds an existing policy or creates a new one.
func resolveCertificatePolicy(ctx context.Context, podName, adminJWT, projectID string) (string, error) {
	policies, err := listCertificatePolicies(ctx, podName, adminJWT, projectID)
	if err != nil {
		return "", err
	}
	if len(policies) > 0 {
		fmt.Printf("[infisical-bootstrap] Using existing policy: id=%s name=%s\n", policies[0].ID, policies[0].Name)
		return policies[0].ID, nil
	}

	policyName := "zero-ops-platform-policy"
	policyID, err := createCertificatePolicy(ctx, podName, adminJWT, projectID, policyName)
	if err != nil {
		return "", fmt.Errorf("create policy %q: %w", policyName, err)
	}
	fmt.Printf("[infisical-bootstrap] Created policy: id=%s name=%s\n", policyID, policyName)
	return policyID, nil
}

// createFleetIntermediateCA creates a root CA named "Fleet Intermediate CA"
// in the given project via POST /api/v1/cert-manager/ca/internal.
// A root CA becomes active immediately when notAfter is provided — without
// it the CA stays at "pending-certificate" and cannot issue certificates.
func createFleetIntermediateCA(ctx context.Context, podName, adminJWT, projectID string) (string, error) {
	body := fmt.Sprintf(
		`{"name":"fleet-intermediate-ca","projectId":"%s","status":"active","configuration":{"type":"root","commonName":"%s","organization":"Zero-Ops","ou":"","country":"","province":"","locality":"","maxPathLength":1,"keyAlgorithm":"RSA_2048"},"notAfter":"2036-06-07T00:00:00Z"}`,
		projectID, fleetCAName,
	)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+PathCertificateAuthoritiesCreate,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return "", fmt.Errorf("curl failed: %w", err)
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return "", fmt.Errorf("parse create CA response: %w\nresponse: %s", err, output)
	}
	if resp.ID == "" {
		return "", fmt.Errorf("create CA returned empty ID: %s", output)
	}
	return resp.ID, nil
}

// activateRootCA generates a self-signed certificate for the root CA,
// transitioning it from "pending-certificate" to "active" status. Root
// CAs are stuck at "pending-certificate" until this step — without it
// the cert-operator gets "CA is not active" on bootstrap cert issuance.
func activateRootCA(ctx context.Context, podName, adminJWT, caID string) error {
	body := `{"notBefore":"2026-06-07T00:00:00Z","notAfter":"2036-06-07T00:00:00Z","maxPathLength":1}`
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "POST",
		"http://localhost:"+infisicalPort+fmt.Sprintf(PathCACertificate, caID),
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return fmt.Errorf("generate CA certificate failed: %w\noutput: %s", err, output)
	}
	return nil
}

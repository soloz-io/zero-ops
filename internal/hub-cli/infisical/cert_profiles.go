package infisical

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const certProfileSlug = "argocd-bootstrap"
const fleetCAName = "Fleet Intermediate CA"

type caEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type policyEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ensureArgocdBootstrapProfile creates the argocd-bootstrap certificate profile if it does not exist.
// It looks up the Fleet Intermediate CA, finds or creates a certificate policy, then creates the profile.
// This replaces the manual Step 3.6 UI action from the old workflow.
func ensureArgocdBootstrapProfile(ctx context.Context, podName, adminJWT, projectID string) error {
	found, err := CheckCertificateProfile(ctx, podName, adminJWT, projectID, certProfileSlug)
	if err != nil {
		return fmt.Errorf("check profile %q: %w", certProfileSlug, err)
	}
	if found {
		fmt.Printf("[infisical-bootstrap] ✓ Certificate profile %q already exists\n", certProfileSlug)
		return nil
	}

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
		return fmt.Errorf("CA %q not found in project %s", fleetCAName, projectID)
	}
	fmt.Printf("[infisical-bootstrap] Found CA %q: id=%s\n", fleetCAName, fleetCAID)

	policyID, err := resolveCertificatePolicy(ctx, podName, adminJWT, projectID)
	if err != nil {
		return fmt.Errorf("resolve certificate policy: %w", err)
	}

	if err := createCertProfile(ctx, podName, adminJWT, projectID, fleetCAID, policyID, certProfileSlug, 3650); err != nil {
		return fmt.Errorf("create certificate profile %q: %w", certProfileSlug, err)
	}
	fmt.Printf("[infisical-bootstrap] ✓ Certificate profile %q created\n", certProfileSlug)
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
func createCertificatePolicy(ctx context.Context, podName, adminJWT, projectID, name string) (string, error) {
	body := fmt.Sprintf(`{"projectId":"%s","name":"%s"}`, projectID, name)
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

	policyName := certProfileSlug + "-policy"
	policyID, err := createCertificatePolicy(ctx, podName, adminJWT, projectID, policyName)
	if err != nil {
		return "", fmt.Errorf("create policy %q: %w", policyName, err)
	}
	fmt.Printf("[infisical-bootstrap] Created policy: id=%s name=%s\n", policyID, policyName)
	return policyID, nil
}

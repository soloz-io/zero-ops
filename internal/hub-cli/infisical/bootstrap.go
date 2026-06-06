package infisical

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	adminEmail    = "admin@nutgraf.in"
	adminPassword = "secretzero123"
	orgName       = "Zero-Ops"

	identityName  = "hub-platform-eso"

	infisicalNamespace = "platform-security"
	infisicalPort      = "8080"
)

var cachedBootstrap *bootstrapOutput

type BootstrapResult struct {
	OrgID        string
	ProjectID    string
	ProjectSlug  string
	ClientID     string
	ClientSecret string
}

// bootstrapOutput maps the actual `infisical bootstrap --output json` response.
// Verified on 2026-06-06 against Infisical OSS v0.93.x:
//
//	{
//	  "message": "Successfully bootstrapped instance",
//	  "identity": {
//	    "id": "<uuid>",
//	    "name": "Instance Admin Identity",
//	    "credentials": { "token": "<jwt>" }
//	  },
//	  "organization": {
//	    "id": "<uuid>",
//	    "name": "Zero-Ops",
//	    "slug": "zero-ops",
//	    "defaultMembershipRole": "admin",
//	    "authEnforced": false,
//	    "scimEnabled": true
//	  }
//	}
type bootstrapOutput struct {
	Message      string `json:"message"`
	Identity     struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Credentials struct {
			Token string `json:"token"`
		} `json:"credentials"`
	} `json:"identity"`
	Organization struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"organization"`
}

type createProjectResponse struct {
	Project struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"project"`
}

type createIdentityResponse struct {
	Identity struct {
		ID string `json:"id"`
	} `json:"identity"`
}

type attachUniversalAuthResponse struct {
	IdentityUniversalAuth struct {
		ClientID   string `json:"clientId"`
		IdentityID string `json:"identityId"`
	} `json:"identityUniversalAuth"`
}

type createClientSecretResponse struct {
	ClientSecret string `json:"clientSecret"`
}

func kubectlExec(ctx context.Context, podName string, args ...string) (string, error) {
	cmdArgs := append([]string{
		"exec", "-n", infisicalNamespace, "pod/" + podName,
		"--",
	}, args...)
	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl exec failed: %w\noutput: %s", err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func GetInfisicalPodName(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "pods", "-n", infisicalNamespace,
		"-l", "app=infisical-standalone,component=infisical",
		"-o", "jsonpath={.items[0].metadata.name}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get infisical pod name: %w\noutput: %s", err, string(output))
	}
	name := strings.TrimSpace(string(output))
	if name == "" {
		return "", fmt.Errorf("no infisical pod found in namespace %s", infisicalNamespace)
	}
	return name, nil
}

func runBootstrap(ctx context.Context, podName string) (*bootstrapOutput, error) {
	if cachedBootstrap != nil {
		fmt.Println("[infisical-bootstrap] Using cached bootstrap result")
		return cachedBootstrap, nil
	}

	output, err := kubectlExec(ctx, podName,
		"infisical", "bootstrap",
		"--email", adminEmail,
		"--password", adminPassword,
		"--organization", orgName,
		"--domain", "http://localhost:"+infisicalPort,
		"--ignore-if-bootstrapped",
		"--output", "json",
		"--silent",
	)
	if err != nil {
		return nil, fmt.Errorf("infisical bootstrap failed: %w", err)
	}

	// The CLI may return empty output on first call (stderr-only progress) or
	// on success when already bootstrapped. If empty, run again with --output json.
	if output == "" {
		output, err = kubectlExec(ctx, podName,
			"infisical", "bootstrap",
			"--email", adminEmail,
			"--password", adminPassword,
			"--organization", orgName,
			"--domain", "http://localhost:"+infisicalPort,
			"--ignore-if-bootstrapped",
			"--output", "json",
			"--silent",
		)
		if err != nil {
			return nil, fmt.Errorf("infisical bootstrap (retry) failed: %w", err)
		}
		if output == "" {
			return nil, fmt.Errorf("infisical bootstrap returned empty output on retry")
		}
	}

	var result bootstrapOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse infisical bootstrap output: %w\noutput: %s", err, output)
	}

	if result.Identity.Credentials.Token == "" {
		return nil, fmt.Errorf("infisical bootstrap returned no identity token: %s", output)
	}
	if result.Organization.ID == "" {
		return nil, fmt.Errorf("infisical bootstrap returned no organization ID: %s", output)
	}

	cachedBootstrap = &result
	fmt.Printf("[infisical-bootstrap] Bootstrap complete: org=%s (%s)\n", result.Organization.Name, result.Organization.ID)
	return cachedBootstrap, nil
}

func createProject(ctx context.Context, podName, adminJWT string) (string, string, error) {
	payload := map[string]any{
		"projectName": ProjectSlug,
		"slug":        ProjectSlug,
		"type":        "cert-manager",
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal create project request: %w", err)
	}
	body := string(bodyBytes)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+PathProjects,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return "", "", fmt.Errorf("create project failed: %w", err)
	}

	// 409 Conflict means project already exists — reconcile by fetching its ID
	var projectResp createProjectResponse
	if err := json.Unmarshal([]byte(output), &projectResp); err != nil {
		return "", "", fmt.Errorf("failed to parse create project response: %w\nresponse: %s", err, output)
	}
	if projectResp.Project.ID == "" {
		// Could be a 409 — try listing projects to find the slug
		listOut, listErr := kubectlExec(ctx, podName,
			"curl", "-s",
			"http://localhost:"+infisicalPort+PathProjects,
			"-H", "Authorization: Bearer "+adminJWT,
		)
		if listErr != nil {
			return "", "", fmt.Errorf("create project ambiguous (empty ID) and list failed: %w", listErr)
		}
		var listResp struct {
			Projects []struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"projects"`
		}
		if err := json.Unmarshal([]byte(listOut), &listResp); err != nil {
			return "", "", fmt.Errorf("create project ambiguous (empty ID) and list parse failed: %w\ndata: %s", err, listOut)
		}
		for _, p := range listResp.Projects {
			if p.Slug == ProjectSlug {
				fmt.Printf("[infisical-bootstrap] Project already exists: %s (id=%s)\n", p.Slug, p.ID)
				return p.ID, p.Slug, nil
			}
		}
		return "", "", fmt.Errorf("create project returned empty ID and project '%s' not found in listing", ProjectSlug)
	}

	fmt.Printf("[infisical-bootstrap] Project created: %s (id=%s)\n", projectResp.Project.Slug, projectResp.Project.ID)
	return projectResp.Project.ID, projectResp.Project.Slug, nil
}

func createMachineIdentity(ctx context.Context, podName, adminJWT, orgID, projectID string) (string, string, error) {
	// 1. Create the Machine Identity
	body := fmt.Sprintf(`{"name":"%s","organizationId":"%s"}`, identityName, orgID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+PathIdentities,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return "", "", fmt.Errorf("create identity failed: %w", err)
	}

	var identityResp createIdentityResponse
	if err := json.Unmarshal([]byte(output), &identityResp); err != nil {
		return "", "", fmt.Errorf("failed to parse create identity response: %w\nresponse: %s", err, output)
	}
	identityID := identityResp.Identity.ID
	if identityID == "" {
		// Identity may already exist — try finding it by name
		found, findErr := findIdentityByName(ctx, podName, adminJWT, identityName, orgID)
		if findErr != nil {
			return "", "", fmt.Errorf("create identity returned empty ID and lookup failed: %w", findErr)
		}
		identityID = found
	}
	fmt.Printf("[infisical-bootstrap] Machine Identity: id=%s\n", identityID)

	// 2. Attach Universal Auth (correct path: /api/v1/auth/universal-auth/identities/{id})
	uaBody := `{"clientSecretTrustedIps":[{"ipAddress":"0.0.0.0/0","type":"ipv4"},{"ipAddress":"::/0","type":"ipv6"}],"accessTokenTrustedIps":[{"ipAddress":"0.0.0.0/0","type":"ipv4"},{"ipAddress":"::/0","type":"ipv6"}],"accessTokenTTL":2592000,"accessTokenMaxTTL":2592000}`
	uaURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathAuthUniversalAuthIdentities, identityID)
	uaOutput, uaErr := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		uaURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", uaBody,
	)
	if uaErr != nil {
		return "", "", fmt.Errorf("attach universal auth failed: %w", uaErr)
	}

	var authResp attachUniversalAuthResponse
	if err := json.Unmarshal([]byte(uaOutput), &authResp); err != nil {
		return "", "", fmt.Errorf("failed to parse universal auth response: %w\nresponse: %s", err, uaOutput)
	}
	clientID := authResp.IdentityUniversalAuth.ClientID
	if clientID == "" {
		// UA may already be configured — try fetching it
		existingUA, getErr := getUniversalAuth(ctx, podName, adminJWT, identityID)
		if getErr != nil {
			return "", "", fmt.Errorf("attach universal auth returned empty clientId and GET failed: %w", getErr)
		}
		clientID = existingUA
	}
	fmt.Printf("[infisical-bootstrap] Universal Auth: clientId=%s\n", clientID)

	// 3. Generate client secret (correct path: /api/v1/auth/universal-auth/identities/{id}/client-secrets)
	csURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathAuthUniversalAuthClientSecrets, identityID)
	csOutput, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		csURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", `{"numUsesLimit":0,"ttl":0}`,
	)
	if err != nil {
		return "", "", fmt.Errorf("generate client secret failed: %w", err)
	}

	var secretResp createClientSecretResponse
	if err := json.Unmarshal([]byte(csOutput), &secretResp); err != nil {
		return "", "", fmt.Errorf("failed to parse client secret response: %w\nresponse: %s", err, csOutput)
	}
	clientSecret := secretResp.ClientSecret
	if clientSecret == "" {
		return "", "", fmt.Errorf("generate client secret returned empty: %s", csOutput)
	}
	fmt.Printf("[infisical-bootstrap] Client secret generated\n")

	// 4. Grant project admin role
	grantURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathProjectMembershipsIdentities, projectID, identityID)
	grantOutput, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		grantURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", `{"roles":[{"role":"admin"}]}`,
	)
	if err != nil {
		fmt.Printf("[infisical-bootstrap] ⚠️  Project membership grant issue: %s\n", err)
	} else {
		fmt.Printf("[infisical-bootstrap] Project admin access granted: %s\n", strings.TrimSpace(grantOutput))
	}

	return clientID, clientSecret, nil
}

// grantProjectAdminRole adds an identity to the project with the admin role.
func grantProjectAdminRole(ctx context.Context, podName, adminJWT, projectID, identityID string) error {
	grantURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathProjectMembershipsIdentities, projectID, identityID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		grantURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", `{"roles":[{"role":"admin"}]}`,
	)
	if err != nil {
		return fmt.Errorf("curl failed: %w", err)
	}
	if strings.Contains(output, "Conflict") || strings.Contains(output, "already exists") {
		fmt.Printf("[infisical-bootstrap] Instance Admin Identity already has project membership\n")
		return nil
	}
	fmt.Printf("[infisical-bootstrap] Instance Admin Identity granted admin role on project\n")
	return nil
}

// findIdentityByName looks up a Machine Identity by name within the organization.
func findIdentityByName(ctx context.Context, podName, adminJWT, name, orgID string) (string, error) {
	output, err := kubectlExec(ctx, podName,
		"curl", "-s",
		fmt.Sprintf("http://localhost:%s%s?limit=100&orgId=%s", infisicalPort, PathIdentities, orgID),
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return "", fmt.Errorf("list identities failed: %w", err)
	}

	var listResp struct {
		Identities []struct {
			Identity struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"identity"`
		} `json:"identities"`
	}
	if err := json.Unmarshal([]byte(output), &listResp); err != nil {
		return "", fmt.Errorf("parse identities list: %w\nresponse: %s", err, output)
	}

	for _, item := range listResp.Identities {
		if item.Identity.Name == name {
			return item.Identity.ID, nil
		}
	}
	return "", fmt.Errorf("identity '%s' not found in org %s", name, orgID)
}

// getUniversalAuth retrieves the clientId from the Universal Auth configuration.
func getUniversalAuth(ctx context.Context, podName, adminJWT, identityID string) (string, error) {
	uaURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathAuthUniversalAuthIdentities, identityID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s",
		uaURL,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return "", fmt.Errorf("get universal auth failed: %w", err)
	}

	var uaResp struct {
		IdentityUniversalAuth struct {
			ClientID string `json:"clientId"`
		} `json:"identityUniversalAuth"`
	}
	if err := json.Unmarshal([]byte(output), &uaResp); err != nil {
		return "", fmt.Errorf("parse universal auth: %w\nresponse: %s", err, output)
	}
	if uaResp.IdentityUniversalAuth.ClientID == "" {
		return "", fmt.Errorf("universal auth has no clientId: %s", output)
	}
	return uaResp.IdentityUniversalAuth.ClientID, nil
}

// GetBootstrapToken re-runs the idempotent infisical bootstrap CLI and returns
// the admin identity token + organization ID. Call this when you need a fresh
// admin-scoped token for post-bootstrap health gates (e.g., cert-manager profile check).
// The bootstrap is a no-op if already bootstrapped (--ignore-if-bootstrapped).
func GetBootstrapToken(ctx context.Context, podName string) (adminJWT, orgID string, err error) {
	boot, err := runBootstrap(ctx, podName)
	if err != nil {
		return "", "", err
	}
	return boot.Identity.Credentials.Token, boot.Organization.ID, nil
}

// FindProjectBySlug lists all Infisical projects and returns the UUID of the
// one matching the given slug. Uses admin bootstrap token for auth.
func FindProjectBySlug(ctx context.Context, podName, adminJWT, slug string) (string, error) {
	output, err := kubectlExec(ctx, podName,
		"curl", "-s",
		"http://localhost:"+infisicalPort+PathProjects,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return "", fmt.Errorf("list projects failed: %w", err)
	}
	var listResp struct {
		Projects []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(output), &listResp); err != nil {
		return "", fmt.Errorf("parse projects list: %w\nresponse: %s", err, output)
	}
	for _, p := range listResp.Projects {
		if p.Slug == slug {
			fmt.Printf("[infisical-bootstrap] Found project '%s' (id=%s)\n", p.Slug, p.ID)
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project '%s' not found", slug)
}

// CheckCertificateProfile verifies that a cert-manager profile exists in Infisical.
// The projectId query parameter is required by Infisical OSS to scope the lookup
// (source: certificate-profiles-router.ts:461).
func CheckCertificateProfile(ctx context.Context, podName, adminJWT, projectID, slug string) (bool, error) {
	profilePath := fmt.Sprintf(PathCertificateProfilesBySlug, slug, projectID)
	profileURL := "http://localhost:" + infisicalPort + profilePath
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
		profileURL,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return false, fmt.Errorf("check profile %s: %w", slug, err)
	}
	code := strings.TrimSpace(output)
	if code == "200" {
		return true, nil
	}
	if code == "404" {
		return false, nil
	}
	return false, fmt.Errorf("unexpected status code %s checking profile %s", code, slug)
}

// WaitForCertificateProfile polls for the existence of a cert-manager profile.
// If the profile is not found, it prints clear manual instructions for the UI step
// and retries until the timeout expires.
func WaitForCertificateProfile(ctx context.Context, podName, adminJWT, projectID, slug string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	printed := false

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		found, err := CheckCertificateProfile(ctx, podName, adminJWT, projectID, slug)
		if err != nil {
			fmt.Printf("[infisical-bootstrap] ⚠️  Profile check error (retrying): %v\n", err)
			time.Sleep(15 * time.Second)
			continue
		}
		if found {
			fmt.Printf("[infisical-bootstrap] ✓ Certificate profile '%s' found\n", slug)
			return nil
		}

		if !printed {
			fmt.Println()
			fmt.Println("╔══════════════════════════════════════════════════════════════════════════╗")
			fmt.Println("║  MANUAL ACTION REQUIRED:  Create the 'argocd-bootstrap' profile         ║")
			fmt.Println("╚══════════════════════════════════════════════════════════════════════════╝")
			fmt.Println()
			fmt.Println("  1. Port-forward Infisical:")
			fmt.Println("     kubectl port-forward -n platform-security \\")
			fmt.Println("       svc/infisical-standalone-infisical 8080:8080")
			fmt.Println()
			fmt.Println("  2. Open http://localhost:8080 in your browser")
			fmt.Println("  3. Log in as admin@nutgraf.in / secretzero123")
			fmt.Println("  4. Navigate to: Certificates → Profiles → Create Profile")
			fmt.Println("  5. Slug: argocd-bootstrap")
			fmt.Println("  6. CA: Fleet Intermediate CA (NOT platform-db-ca — per ADR-035)")
			fmt.Println("  7. Validity: 10 years")
			fmt.Println("  8. Click 'Create'")
			fmt.Println()
			fmt.Println("  Waiting for profile to appear... (timeout: " + timeout.Round(time.Second).String() + ")")
			printed = true
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}

	return fmt.Errorf("timeout after %v waiting for certificate profile '%s' — create it in the Infisical UI, then re-run 'hub init-secrets'", timeout, slug)
}

func BootstrapInfisicalDayZero(ctx context.Context) (*BootstrapResult, error) {
	fmt.Println("[infisical-bootstrap] Starting Infisical Day-0 bootstrap...")

	podName, err := GetInfisicalPodName(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot find Infisical pod: %w", err)
	}
	fmt.Printf("[infisical-bootstrap] Using pod: %s\n", podName)

	boot, err := runBootstrap(ctx, podName)
	if err != nil {
		return nil, err
	}

	orgID := boot.Organization.ID
	fmt.Printf("[infisical-bootstrap] Organization ID: %s\n", orgID)

	projectID, projectSlug, err := createProject(ctx, podName, boot.Identity.Credentials.Token)
	if err != nil {
		return nil, err
	}

	// Grant project admin role to the Instance Admin Identity so it can perform API operations
	// on the project (e.g., listing CAs, creating certificate profiles in Step 3.6).
	if err := grantProjectAdminRole(ctx, podName, boot.Identity.Credentials.Token, projectID, boot.Identity.ID); err != nil {
		return nil, fmt.Errorf("grant project admin to instance admin identity: %w", err)
	}

	clientID, clientSecret, err := createMachineIdentity(ctx, podName, boot.Identity.Credentials.Token, orgID, projectID)
	if err != nil {
		return nil, err
	}

	// Automate Step 3.6: create argocd-bootstrap certificate profile
	if err := ensureArgocdBootstrapProfile(ctx, podName, boot.Identity.Credentials.Token, projectID); err != nil {
		return nil, fmt.Errorf("ensure argocd-bootstrap profile: %w", err)
	}

	fmt.Println("[infisical-bootstrap] ✅ Infisical Day-0 bootstrap complete")
	return &BootstrapResult{
		OrgID:        orgID,
		ProjectID:    projectID,
		ProjectSlug:  projectSlug,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}

func BootstrapInfisicalDayZeroWithRetry(ctx context.Context, timeout time.Duration) (*BootstrapResult, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		result, err := BootstrapInfisicalDayZero(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		fmt.Printf("[infisical-bootstrap] ⚠️  Bootstrap attempt failed: %v\n", err)
		fmt.Println("[infisical-bootstrap] Retrying in 10 seconds...")

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}

	return nil, fmt.Errorf("infisical bootstrap failed after %v: %w", timeout, lastErr)
}



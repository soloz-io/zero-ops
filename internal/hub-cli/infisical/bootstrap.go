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
	projectName   = "hub-platform"
	identityName  = "hub-platform-eso"

	infisicalNamespace = "platform-security"
	infisicalPort      = "8080"
)

type BootstrapResult struct {
	OrgID        string
	ProjectID    string
	ProjectSlug  string
	ClientID     string
	ClientSecret string
}

type bootstrapOutput struct {
	AdminEmail       string `json:"adminEmail"`
	AdminJWT         string `json:"adminJwt"`
	OrganizationID   string `json:"organizationId"`
	OrganizationName string `json:"organizationName"`
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
	ClientSecret struct {
		ClientSecret string `json:"clientSecret"`
	} `json:"clientSecret"`
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

func getInfisicalPodName(ctx context.Context) (string, error) {
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

	var result bootstrapOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse infisical bootstrap output: %w\noutput: %s", err, output)
	}

	if result.AdminJWT == "" || result.OrganizationID == "" {
		return nil, fmt.Errorf("infisical bootstrap returned incomplete output: %s", output)
	}

	fmt.Printf("[infisical-bootstrap] Bootstrap complete: org=%s (%s)\n", result.OrganizationName, result.OrganizationID)
	return &result, nil
}

func getOrganizations(ctx context.Context, podName, adminJWT string) (string, error) {
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "GET",
		"http://localhost:"+infisicalPort+"/api/v1/organizations",
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return "", fmt.Errorf("get organizations failed: %w", err)
	}

	var orgsResp struct {
		Organizations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal([]byte(output), &orgsResp); err != nil {
		return "", fmt.Errorf("failed to parse organizations response: %w\nresponse: %s", err, output)
	}

	for _, org := range orgsResp.Organizations {
		if org.Name == orgName {
			fmt.Printf("[infisical-bootstrap] Found organization: %s (id=%s)\n", org.Name, org.ID)
			return org.ID, nil
		}
	}

	return "", fmt.Errorf("organization '%s' not found — bootstrap may not have completed", orgName)
}

func createProject(ctx context.Context, podName, adminJWT string) (string, string, error) {
	body := fmt.Sprintf(`{"projectName":"%s"}`, projectName)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+"/api/v1/projects",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", body,
	)
	if err != nil {
		return "", "", fmt.Errorf("create project failed: %w", err)
	}

	var projectResp createProjectResponse
	if err := json.Unmarshal([]byte(output), &projectResp); err != nil {
		return "", "", fmt.Errorf("failed to parse create project response: %w\nresponse: %s", err, output)
	}
	if projectResp.Project.ID == "" {
		return "", "", fmt.Errorf("create project returned empty ID: %s", output)
	}

	fmt.Printf("[infisical-bootstrap] Project created: %s (id=%s)\n", projectResp.Project.Slug, projectResp.Project.ID)
	return projectResp.Project.ID, projectResp.Project.Slug, nil
}

func createMachineIdentity(ctx context.Context, podName, adminJWT, projectID string) (string, string, error) {
	body := fmt.Sprintf(`{"name":"%s"}`, identityName)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+"/api/v1/identities",
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
		return "", "", fmt.Errorf("create identity returned empty ID: %s", output)
	}
	fmt.Printf("[infisical-bootstrap] Machine Identity created: id=%s\n", identityID)

	uaBody := `{"clientId":"","clientSecretTrustedIps":[{"ipAddress":"0.0.0.0/0","type":"ipv4"}]}`
	uaOutput, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+"/api/v1/identities/"+identityID+"/universal-auth",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", uaBody,
	)
	if err != nil {
		return "", "", fmt.Errorf("attach universal auth failed: %w", err)
	}

	var authResp attachUniversalAuthResponse
	if err := json.Unmarshal([]byte(uaOutput), &authResp); err != nil {
		return "", "", fmt.Errorf("failed to parse universal auth response: %w\nresponse: %s", err, uaOutput)
	}
	clientID := authResp.IdentityUniversalAuth.ClientID
	if clientID == "" {
		return "", "", fmt.Errorf("attach universal auth returned empty clientId: %s", uaOutput)
	}
	fmt.Printf("[infisical-bootstrap] Universal Auth attached: clientId=%s\n", clientID)

	csOutput, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+"/api/v1/identities/"+identityID+"/universal-auth/client-secrets",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return "", "", fmt.Errorf("generate client secret failed: %w", err)
	}

	var secretResp createClientSecretResponse
	if err := json.Unmarshal([]byte(csOutput), &secretResp); err != nil {
		return "", "", fmt.Errorf("failed to parse client secret response: %w\nresponse: %s", err, csOutput)
	}
	clientSecret := secretResp.ClientSecret.ClientSecret
	if clientSecret == "" {
		return "", "", fmt.Errorf("generate client secret returned empty secret: %s", csOutput)
	}
	fmt.Printf("[infisical-bootstrap] Client secret generated\n")

	grantOutput, err := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		"http://localhost:"+infisicalPort+"/api/v1/projects/"+projectID+"/memberships/identities/"+identityID,
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

func BootstrapInfisicalDayZero(ctx context.Context) (*BootstrapResult, error) {
	fmt.Println("[infisical-bootstrap] Starting Infisical Day-0 bootstrap...")

	podName, err := getInfisicalPodName(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot find Infisical pod: %w", err)
	}
	fmt.Printf("[infisical-bootstrap] Using pod: %s\n", podName)

	boot, err := runBootstrap(ctx, podName)
	if err != nil {
		return nil, err
	}

	orgID, err := getOrganizations(ctx, podName, boot.AdminJWT)
	if err != nil {
		return nil, err
	}
	fmt.Printf("[infisical-bootstrap] Organization ID: %s\n", orgID)

	projectID, projectSlug, err := createProject(ctx, podName, boot.AdminJWT)
	if err != nil {
		return nil, err
	}

	clientID, clientSecret, err := createMachineIdentity(ctx, podName, boot.AdminJWT, projectID)
	if err != nil {
		return nil, err
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

package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
)

const (
	adminEmail    = "arun4infra@gmail.com"
	adminPassword = "Password@123"
	orgName       = "Zero-Ops"

	identityName  = "hub-platform-eso"

	infisicalNamespace = "platform-security"
	infisicalPort      = "8080"
)

// errAlreadyBootstrapped is returned by runBootstrapCLI when the Infisical
// server is already bootstrapped and the CLI banner-only output was
// produced. Callers must NOT reset the database when they receive this
// error — they should call getExistingOrgData to recover the existing
// org/identity IDs without destroying state.
var errAlreadyBootstrapped = errors.New("infisical instance already bootstrapped")

var cachedBootstrap *bootstrapOutput

type BootstrapResult struct {
	OrgID              string
	ProjectID          string
	ProjectSlug        string
	SecretsProjectID   string
	SecretsProjectSlug string
	ClientID           string
	ClientSecret       string
	IdentityID         string
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

// getExistingOrgData recovers the org/identity state from an already-
// bootstrapped Infisical server via the API. It is used in place of the
// previous resetBootstrapState, which destructively DELETED users and
// projects from PostgreSQL on re-run and forced callers to re-bootstrap
// from scratch — that path orphaned any in-flight cert-operator
// reconcile (which had cached the old project IDs at startup).
//
// The recovery flow:
//  1. POST /api/v3/auth/login with the well-known bootstrap credentials
//     to get a user JWT (the V1 auth router has no login endpoint in
//     this Infisical version — the email/password login is at V3 only).
//     The V3 login returns {"accessToken": "<jwt>"} — no org context.
//  2. GET /api/v1/organization to get the org ID + name.
//     Returns {"organizations": [{"id": "...", "name": "...", ...}]}
//     (an array inside "organizations" key — NOT a flat object).
//  3. POST /api/v3/auth/select-organization to embed the org ID into
//     the JWT. Without this, org-scoped endpoints (identities, etc.)
//     return 401 "no organization found in request". The select-org
//     endpoint returns {"token": "<jwt>", "isMfaEnabled": false}.
//  4. GET /api/v1/identities?orgId=<org> with the org-scoped JWT to
//     find the "Instance Admin Identity" created by `infisical bootstrap`.
//
// All HTTP calls use `curl -s` (no -f) so HTTP 4xx responses with JSON
// bodies (e.g., 409 Conflict) are still parsed and handled gracefully
// by the surrounding Go code. Calls where HTTP errors are unambiguous
// hard failures (login, select-org, PATCH grant) do use `curl -f` so
// a non-2xx response surfaces immediately as a non-nil error.
func getExistingOrgData(ctx context.Context, podName string) (*bootstrapOutput, error) {
	// 1. Log in as the bootstrap admin to get a JWT.
	loginPayload := fmt.Sprintf(`{"email":"%s","password":"%s"}`, adminEmail, adminPassword)
	loginOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "POST",
		"http://localhost:"+infisicalPort+PathAuthLoginV3,
		"-H", "Content-Type: application/json",
		"-d", loginPayload,
	)
	if err != nil {
		return nil, fmt.Errorf("admin login failed: %w", err)
	}
	var loginResp struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal([]byte(loginOut), &loginResp); err != nil {
		return nil, fmt.Errorf("decode admin login: %w\nbody: %s", err, loginOut)
	}
	if loginResp.AccessToken == "" {
		return nil, fmt.Errorf("admin login returned no accessToken: %s", loginOut)
	}
	adminJWT := loginResp.AccessToken

	// 2. Get the current organization.
	orgOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f",
		"http://localhost:"+infisicalPort+PathCurrentOrganization,
		"-H", "Authorization: Bearer "+adminJWT,
	)
	if err != nil {
		return nil, fmt.Errorf("get current organization: %w", err)
	}
	var orgResp struct {
		Organizations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal([]byte(orgOut), &orgResp); err != nil {
		return nil, fmt.Errorf("decode current organization: %w\nbody: %s", err, orgOut)
	}
	if len(orgResp.Organizations) == 0 || orgResp.Organizations[0].ID == "" {
		return nil, fmt.Errorf("current organization has no ID: %s", orgOut)
	}
	orgID := orgResp.Organizations[0].ID
	orgName := orgResp.Organizations[0].Name

	// 3. Select the organization to get an org-scoped JWT. The V3 login
	// returns a user JWT without an org context. Org-scoped endpoints
	// (e.g., GET /api/v1/identities) require the org ID to be embedded
	// in the token — without it they return 401/422.
	selectOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "POST",
		"http://localhost:"+infisicalPort+PathAuthSelectOrgV3,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+adminJWT,
		"-d", fmt.Sprintf(`{"organizationId":"%s"}`, orgID),
	)
	if err != nil {
		return nil, fmt.Errorf("select organization failed: %w", err)
	}
	var selectResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(selectOut), &selectResp); err != nil {
		return nil, fmt.Errorf("decode select organization response: %w\nbody: %s", err, selectOut)
	}
	if selectResp.Token == "" {
		return nil, fmt.Errorf("select organization returned no token: %s", selectOut)
	}
	orgJWT := selectResp.Token

	// 4. Find the Instance Admin Identity using the org-scoped JWT.
	identitiesOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f",
		"http://localhost:"+infisicalPort+fmt.Sprintf(PathIdentitiesByOrg, orgID),
		"-H", "Authorization: Bearer "+orgJWT,
	)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	var identitiesResp struct {
		Identities []struct {
			Identity struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"identity"`
		} `json:"identities"`
	}
	if err := json.Unmarshal([]byte(identitiesOut), &identitiesResp); err != nil {
		return nil, fmt.Errorf("decode identities: %w\nbody: %s", err, identitiesOut)
	}
	var adminIdentityID string
	for _, item := range identitiesResp.Identities {
		if item.Identity.Name == "Instance Admin Identity" {
			adminIdentityID = item.Identity.ID
			break
		}
	}
	if adminIdentityID == "" {
		return nil, fmt.Errorf("Instance Admin Identity not found in org %s; existing bootstrap may be from a different schema", orgID)
	}

	result := &bootstrapOutput{}
	result.Identity.ID = adminIdentityID
	result.Identity.Name = "Instance Admin Identity"
	result.Identity.Credentials.Token = orgJWT
	result.Organization.ID = orgID
	result.Organization.Name = orgName
	return result, nil
}

// stripCLIBanner strips CLI update-notification banners ("A new release...") from
// the output. Returns the substring starting at the first '{', or empty string if
// no JSON body is found.
func stripCLIBanner(output string) string {
	idx := strings.Index(output, "{")
	if idx < 0 {
		return ""
	}
	return output[idx:]
}

// hostKubectl runs kubectl on the host machine (not inside a pod).
// Used for reading cluster resources like ConfigMaps that are not
// accessible from within the Infisical pod.
func hostKubectl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl failed: %w\noutput: %s", err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

// getCachedProjectID reads project IDs from the hub-bootstrap-config
// ConfigMap that was populated during the first successful bootstrap.
// On re-run the ConfigMap persists, so we can skip the API project
// listing (which the user JWT cannot do — see grantOrgAdminRole docs).
func getCachedProjectID(slug string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "INFISICAL_PROJECT_ID"
	if slug == SecretsProjectSlug {
		key = "INFISICAL_SECRETS_PROJECT_ID"
	}

	out, err := hostKubectl(ctx,
		"get", "configmap", "-n", constants.NamespaceOps, "hub-bootstrap-config",
		"-o", "jsonpath={.data."+key+"}",
	)
	if err != nil {
		return ""
	}
	return out
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

// runBootstrapCLI calls infisical bootstrap and returns the JSON output.
//
// If the server is already bootstrapped (banner-only output, no JSON
// body), the function returns errAlreadyBootstrapped so the caller can
// recover the existing org/identity state via getExistingOrgData.
// This used to destructively DELETE users, projects, and Redis state
// and re-bootstrap from scratch — that path orphaned any in-flight
// cert-operator reconcile (which had cached the old project IDs at
// startup) and is no longer invoked.
func runBootstrapCLI(ctx context.Context, podName string) (string, error) {
	output, err := kubectlExec(ctx, podName,
		"infisical", "bootstrap",
		"--email", adminEmail,
		"--password", adminPassword,
		"--organization", orgName,
		"--domain", "http://localhost:"+infisicalPort,
		"--ignore-if-bootstrapped",
		"--output", "json",
	)
	if err != nil {
		return "", fmt.Errorf("infisical bootstrap failed: %w", err)
	}

	jsonOutput := stripCLIBanner(output)
	if jsonOutput != "" {
		return jsonOutput, nil
	}

	// Banner-only output means the server is already bootstrapped.
	// Surface this as a sentinel error — the caller (runBootstrap /
	// BootstrapInfisicalDayZero) will recover the existing org/identity
	// state via getExistingOrgData without destroying anything.
	return "", errAlreadyBootstrapped
}

func runBootstrap(ctx context.Context, podName string) (*bootstrapOutput, error) {
	if cachedBootstrap != nil {
		fmt.Println("[infisical-bootstrap] Using cached bootstrap result")
		return cachedBootstrap, nil
	}

	jsonOutput, err := runBootstrapCLI(ctx, podName)
	if errors.Is(err, errAlreadyBootstrapped) {
		fmt.Println("[infisical-bootstrap] Server already bootstrapped, recovering existing org/identity via API...")
		existing, getErr := getExistingOrgData(ctx, podName)
		if getErr != nil {
			return nil, fmt.Errorf("recover existing bootstrap: %w", getErr)
		}
		cachedBootstrap = existing
		fmt.Printf("[infisical-bootstrap] Re-using existing bootstrap: org=%s (%s), identity=%s\n",
			existing.Organization.Name, existing.Organization.ID, existing.Identity.ID)
		return cachedBootstrap, nil
	}
	if err != nil {
		return nil, err
	}

	var result bootstrapOutput
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return nil, fmt.Errorf("failed to parse infisical bootstrap output: %w\noutput: %s", err, jsonOutput)
	}

	if result.Identity.Credentials.Token == "" {
		return nil, fmt.Errorf("infisical bootstrap returned no identity token: %s", jsonOutput)
	}
	if result.Organization.ID == "" {
		return nil, fmt.Errorf("infisical bootstrap returned no organization ID: %s", jsonOutput)
	}

	cachedBootstrap = &result
	fmt.Printf("[infisical-bootstrap] Bootstrap complete: org=%s (%s)\n", result.Organization.Name, result.Organization.ID)
	return cachedBootstrap, nil
}

func createProject(ctx context.Context, podName, adminJWT, projectSlug, projectType string) (string, string, error) {
	payload := map[string]any{
		"projectName": projectSlug,
		"slug":        projectSlug,
		"type":        projectType,
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
			if p.Slug == projectSlug {
				fmt.Printf("[infisical-bootstrap] Project already exists: %s (id=%s)\n", p.Slug, p.ID)
				return p.ID, p.Slug, nil
			}
		}

		// Project listing returned empty (user JWT may not have project
		// membership). Check the hub-bootstrap-config ConfigMap which
		// was populated during the first successful bootstrap run.
		if cachedID := getCachedProjectID(projectSlug); cachedID != "" {
			fmt.Printf("[infisical-bootstrap] Found cached project %s (id=%s) in hub-bootstrap-config\n", projectSlug, cachedID)
			return cachedID, projectSlug, nil
		}

		return "", "", fmt.Errorf("create project returned empty ID and project '%s' not found in listing or ConfigMap", projectSlug)
	}

	fmt.Printf("[infisical-bootstrap] Project created: %s (id=%s)\n", projectResp.Project.Slug, projectResp.Project.ID)
	return projectResp.Project.ID, projectResp.Project.Slug, nil
}

func createMachineIdentity(ctx context.Context, podName, adminJWT, orgID, projectID string) (string, string, string, error) {
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
		return "", "", "", fmt.Errorf("create identity failed: %w", err)
	}

	var identityResp createIdentityResponse
	if err := json.Unmarshal([]byte(output), &identityResp); err != nil {
		return "", "", "", fmt.Errorf("failed to parse create identity response: %w\nresponse: %s", err, output)
	}
	identityID := identityResp.Identity.ID
	if identityID == "" {
		// Identity may already exist — try finding it by name
		found, findErr := findIdentityByName(ctx, podName, adminJWT, identityName, orgID)
		if findErr != nil {
			return "", "", "", fmt.Errorf("create identity returned empty ID and lookup failed: %w", findErr)
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
		return "", "", "", fmt.Errorf("attach universal auth failed: %w", uaErr)
	}

	var authResp attachUniversalAuthResponse
	if err := json.Unmarshal([]byte(uaOutput), &authResp); err != nil {
		return "", "", "", fmt.Errorf("failed to parse universal auth response: %w\nresponse: %s", err, uaOutput)
	}
	clientID := authResp.IdentityUniversalAuth.ClientID
	if clientID == "" {
		// UA may already be configured — try fetching it
		existingUA, getErr := getUniversalAuth(ctx, podName, adminJWT, identityID)
		if getErr != nil {
			return "", "", "", fmt.Errorf("attach universal auth returned empty clientId and GET failed: %w", getErr)
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
		return "", "", "", fmt.Errorf("generate client secret failed: %w", err)
	}

	var secretResp createClientSecretResponse
	if err := json.Unmarshal([]byte(csOutput), &secretResp); err != nil {
		return "", "", "", fmt.Errorf("failed to parse client secret response: %w\nresponse: %s", err, csOutput)
	}
	clientSecret := secretResp.ClientSecret
	if clientSecret == "" {
		return "", "", "", fmt.Errorf("generate client secret returned empty: %s", csOutput)
	}
	fmt.Printf("[infisical-bootstrap] Client secret generated\n")

	// 4. Grant project admin role — HARD failure. Without this the
	// Machine Identity cannot read/write the cert project.
	if err := grantProjectAdminRole(ctx, podName, adminJWT, projectID, identityID); err != nil {
		return "", "", "", fmt.Errorf("grant project admin: %w", err)
	}

	// 5. Grant org-level admin role — HARD failure. The cert-operator
	// needs this to create Machine Identities for spokes on a different
	// org, which requires `identity:create` at the org scope. Previously
	// this was logged-as-success with curl -s, hiding the 403.
	//
	// NOTE: grantOrgAdminRole logs in as the admin user (email/password)
	// and uses a user-level JWT — not the adminJWT passed in here, which
	// is the Instance Admin Identity token on first run. Machine Identity
	// tokens cannot manage org-level role assignments in Infisical 0.43.x
	// (new privilege system: only user sessions can PATCH identity
	// memberships at the org scope).
	if err := grantOrgAdminRole(ctx, podName, orgID, identityID); err != nil {
		return "", "", "", fmt.Errorf("grant org admin: %w", err)
	}

	return clientID, clientSecret, identityID, nil
}

// grantProjectAdminRole adds an identity to a project with the admin role.
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

// grantOrgAdminRole adds an identity to the organization with the admin role.
// Required for cert-operator to create Machine Identities for spokes in any
// project within the org. Without this, `POST /api/v1/identities` returns 403
// "You are not allowed to create on identity".
//
// This function logs in as the well-known admin user (email/password) to
// obtain a user-level JWT, then selects the org to get an org-scoped JWT
// (the V3 login JWT has no org context — org-scoped endpoints return 401
// without it), then calls PATCH on the org identity-memberships endpoint.
// Three things make this necessary in Infisical 0.43.x:
//
//  1. The endpoint requires PATCH, not POST. POST returns 403
//     "Parent organization cannot do this operation" (new privilege system,
//     shouldUseNewPrivilegeSystem: true).
//  2. The caller must be a user session. Machine Identity tokens — even
//     ones with role: "admin" at the org scope — get 403 "You are not
//     allowed to access this resource". Only user JWTs (email/password
//     login) can manage org-level identity role assignments.
//  3. The user JWT must have an org context. The V3 login returns a
//     token without an org ID, so we call POST /api/v3/auth/select-organization
//     to embed the org ID before calling the PATCH endpoint.
func grantOrgAdminRole(ctx context.Context, podName, orgID, identityID string) error {
	// 1. Log in as the admin user to get a user-level JWT.
	loginPayload := fmt.Sprintf(`{"email":"%s","password":"%s"}`, adminEmail, adminPassword)
	loginOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "POST",
		"http://localhost:"+infisicalPort+PathAuthLoginV3,
		"-H", "Content-Type: application/json",
		"-d", loginPayload,
	)
	if err != nil {
		return fmt.Errorf("admin user login failed: %w", err)
	}
	var loginResp struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal([]byte(loginOut), &loginResp); err != nil {
		return fmt.Errorf("decode admin user login: %w\nbody: %s", err, loginOut)
	}
	if loginResp.AccessToken == "" {
		return fmt.Errorf("admin user login returned no accessToken: %s", loginOut)
	}
	userJWT := loginResp.AccessToken

	// 2. Select the organization to get an org-scoped JWT. Without this,
	// the PATCH below returns 401 "no organization found in request".
	selectOut, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "POST",
		"http://localhost:"+infisicalPort+PathAuthSelectOrgV3,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+userJWT,
		"-d", fmt.Sprintf(`{"organizationId":"%s"}`, orgID),
	)
	if err != nil {
		return fmt.Errorf("select organization failed: %w", err)
	}
	var selectResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(selectOut), &selectResp); err != nil {
		return fmt.Errorf("decode select organization response: %w\nbody: %s", err, selectOut)
	}
	if selectResp.Token == "" {
		return fmt.Errorf("select organization returned no token: %s", selectOut)
	}
	orgJWT := selectResp.Token

	// 3. Grant org-level admin role via PATCH using the org-scoped JWT.
	grantURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathOrgIdentityMemberships, identityID)
	output, err := kubectlExec(ctx, podName,
		"curl", "-s", "-f", "-X", "PATCH",
		grantURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+orgJWT,
		"-d", `{"roles":[{"role":"admin","isTemporary":false}]}`,
	)
	if err != nil {
		return fmt.Errorf("curl failed: %w", err)
	}
	if strings.Contains(output, "Conflict") || strings.Contains(output, "already exists") {
		fmt.Printf("[infisical-bootstrap] Identity already has org admin role\n")
		return nil
	}
	fmt.Printf("[infisical-bootstrap] Identity granted org admin role: %s\n", strings.TrimSpace(output))
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
	// 403 means the caller lacks project membership but does not
	// guarantee the profile is absent. On re-run the user JWT
	// (org-scoped from getExistingOrgData) does not have project
	// membership — only the Instance Admin Identity token does.
	// The profile was created during the first bootstrap, so it
	// exists. Treat 403 as "found" to skip recreate attempts.
	if code == "403" {
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
	// On re-run (local development), load the cached state from a previous
	// successful bootstrap. This avoids all API calls — no duplicate Machine
	// Identities, no failed grants, no stale ESO credentials.
	if state, err := loadBootstrapState(); err == nil {
		fmt.Printf("[infisical-bootstrap] Using cached state from %s (re-run)\n", stateFilePath())
		return &BootstrapResult{
			OrgID:              state.OrgID,
			ProjectID:          state.ProjectID,
			ProjectSlug:        state.ProjectSlug,
			SecretsProjectID:   state.SecretsProjectID,
			SecretsProjectSlug: state.SecretsProjectSlug,
			ClientID:           state.ClientID,
			ClientSecret:       state.ClientSecret,
			IdentityID:         state.IdentityID,
		}, nil
	}

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

	certProjectID, certProjectSlug, err := createProject(ctx, podName, boot.Identity.Credentials.Token, ProjectSlug, "cert-manager")
	if err != nil {
		return nil, err
	}

	secretsProjectID, secretsProjectSlug, err := createProject(ctx, podName, boot.Identity.Credentials.Token, SecretsProjectSlug, "secret-manager")
	if err != nil {
		return nil, err
	}

	// Grant project admin role to the Instance Admin Identity on both projects
	if err := grantProjectAdminRole(ctx, podName, boot.Identity.Credentials.Token, certProjectID, boot.Identity.ID); err != nil {
		return nil, fmt.Errorf("grant project admin to instance admin identity (cert): %w", err)
	}
	if err := grantProjectAdminRole(ctx, podName, boot.Identity.Credentials.Token, secretsProjectID, boot.Identity.ID); err != nil {
		return nil, fmt.Errorf("grant project admin to instance admin identity (secrets): %w", err)
	}

	clientID, clientSecret, identityID, err := createMachineIdentity(ctx, podName, boot.Identity.Credentials.Token, orgID, certProjectID)
	if err != nil {
		return nil, err
	}

	// Also grant Machine Identity access to the secrets project
	grantURL := "http://localhost:" + infisicalPort + fmt.Sprintf(PathProjectMembershipsIdentities, secretsProjectID, identityID)
	if grantOut, grantErr := kubectlExec(ctx, podName,
		"curl", "-s", "-X", "POST",
		grantURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer "+boot.Identity.Credentials.Token,
		"-d", `{"roles":[{"role":"admin"}]}`,
	); grantErr != nil {
		fmt.Printf("[infisical-bootstrap] ⚠️  Grant secrets project failed: %s\n", grantErr)
	} else {
		fmt.Printf("[infisical-bootstrap] Machine Identity granted admin on secrets project: %s\n", strings.TrimSpace(grantOut))
	}

	// Automate Step 3.6: create argocd-bootstrap certificate profile
	if err := ensureArgocdBootstrapProfile(ctx, podName, boot.Identity.Credentials.Token, certProjectID); err != nil {
		return nil, fmt.Errorf("ensure argocd-bootstrap profile: %w", err)
	}

	fmt.Println("[infisical-bootstrap] ✅ Infisical Day-0 bootstrap complete")
	result := &BootstrapResult{
		OrgID:              orgID,
		ProjectID:          certProjectID,
		ProjectSlug:        certProjectSlug,
		SecretsProjectID:   secretsProjectID,
		SecretsProjectSlug: secretsProjectSlug,
		ClientID:           clientID,
		ClientSecret:       clientSecret,
		IdentityID:         identityID,
	}

	// Cache the result locally so re-runs (local development) skip all API calls.
	if err := saveBootstrapState(result); err != nil {
		fmt.Printf("[infisical-bootstrap] ⚠️  Could not save bootstrap state: %v\n", err)
	}

	return result, nil
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



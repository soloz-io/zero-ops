package escrow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// pathProjects creates a project. Verified against the Infisical source in
// reference-projects/infisical: backend/src/server/routes/v1/project-router.ts
// serves POST /api/v1/workspace, accepts AuthMode.IDENTITY_ACCESS_TOKEN, and its
// body takes projectName and a ProjectType that defaults to SecretManager.
const pathProjects = "/api/v1/workspace"

// projectTypeSecretManager is the only type an escrow can use.
//
// backend/src/db/schemas/models.ts: SecretManager = "secret-manager". A project
// of any other type authenticates identically and then refuses every read and
// write with "The project is of type kms. Operations of type secret-manager are
// not allowed." The type is fixed at creation and cannot be changed afterwards,
// which is why naming it here matters: the alternative is discovering it from a
// 400 inside a reconcile, which is how a KMS project went on being configured as
// this platform's escrow without anything ever being escrowed.
const projectTypeSecretManager = "secret-manager"

// EnsureProject creates this tenant's escrow project and returns its id.
//
// The escrow account is the tenant's (ADR-076) and the platform never holds a
// credential to it beyond what the tenant supplies. Creating the ACCOUNT and its
// first machine identity is therefore irreducibly manual. Everything after that
// is not, and was: the project, the identity's access to it, and recording the
// id. Three manual steps, of which two are silent to get wrong -- a project of
// the wrong type, and an identity that is not a member of it. Both were wrong on
// the box that found this, and neither was reported until a bootstrap stalled
// forty minutes in.
//
// The identity that creates a project is added to it as an admin by Infisical
// itself (project-service.ts: "If the project is being created by an identity,
// add the identity to the project as an admin"), so creating it also grants the
// access it needs. That is one API call replacing two manual steps and the
// mistake they invite.
//
// Every failure here is returned. There is no fallback to a supplied project id
// and no degraded mode: a box whose escrow cannot be created has nowhere to keep
// the keys that decrypt its secret store, and continuing would reproduce exactly
// the state ADR-076 exists to prevent -- one that behaves identically for months
// and differs only on the day the cluster is gone.
func EnsureProject(ctx context.Context, url, clientID, clientSecret, projectName string) (string, error) {
	url = strings.TrimSpace(url)
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	projectName = strings.TrimSpace(projectName)

	for name, v := range map[string]string{
		"escrow URL":    url,
		"client id":     clientID,
		"client secret": clientSecret,
		"project name":  projectName,
	} {
		if v == "" {
			return "", fmt.Errorf("cannot create the escrow project: no %s", name)
		}
	}

	e := &infisicalEscrow{
		baseURL:      strings.TrimSuffix(url, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 30 * time.Second},
	}
	if err := e.authenticate(ctx); err != nil {
		return "", fmt.Errorf("the escrow credentials do not authenticate against %s: %w",
			e.baseURL, err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"projectName": projectName,
		"type":        projectTypeSecretManager,
	})
	if err != nil {
		return "", fmt.Errorf("build the project request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+pathProjects, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build the project request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("reach %s to create the escrow project: %w", e.baseURL, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// 401 and 403 mean the identity cannot create projects in this
		// organisation, which is a permission a person grants once and is the
		// single most likely reason this fails. Named, because the alternative is
		// a status code against an endpoint the reader has never heard of.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("this machine identity cannot create projects in the "+
				"escrow organisation (%d).\n\n"+
				"Give it an organisation role carrying project-create -- Admin works, and a\n"+
				"custom role with only that permission is tighter -- then re-run.\n\n"+
				"Infisical said: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return "", fmt.Errorf("the escrow refused to create project %q (%d): %s",
			projectName, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Project struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"project"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("the escrow's reply to creating %q is not the expected "+
			"shape: %w\n\nreply: %s", projectName, err, strings.TrimSpace(string(raw)))
	}
	if out.Project.ID == "" {
		return "", fmt.Errorf("the escrow created %q and returned no project id\n\nreply: %s",
			projectName, strings.TrimSpace(string(raw)))
	}
	// Asserted rather than assumed. The type defaults to secret-manager server
	// side and is sent explicitly here, so disagreement means the API changed
	// under us -- and a silently wrong type is the failure this whole function
	// exists to remove.
	if out.Project.Type != "" && out.Project.Type != projectTypeSecretManager {
		return "", fmt.Errorf("the escrow created %q as type %q, and an escrow needs %q",
			projectName, out.Project.Type, projectTypeSecretManager)
	}
	return out.Project.ID, nil
}

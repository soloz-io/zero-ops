// Package escrow holds what a box cannot be rebuilt without, outside the box.
//
// At the repository root rather than under the operator that writes it, because
// two things need it and Go's internal rule keeps them apart otherwise: the
// operator escrows, and the CLI retrieves. An escrow only one side can reach is a
// backup with no restore.
package escrow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// EscrowClient holds what a box cannot be rebuilt without, outside the box.
//
// The Infisical master keys are the box's root secret: without them the secret
// store cannot be decrypted, and every credential the platform manages is lost
// with the cluster. They cannot be kept in the box's own Infisical -- that is the
// thing they unlock -- and they cannot be kept by the platform, because ADR-065
// says the platform holds no secret credential belonging to a tenant.
//
// So they go to an Infisical instance the TENANT controls and the platform does
// not: Infisical Cloud, or any Infisical the tenant runs elsewhere. Out-of-band by
// construction, reachable when the cluster is not.
type EscrowClient interface {
	BackupMasterKeys(ctx context.Context, clusterID string, keys interface{}) error
	RestoreMasterKeys(ctx context.Context, clusterID string) (interface{}, error)

	// BackupArtifact and RestoreArtifact hold anything else a box cannot be
	// rebuilt without, under a name of the caller's choosing.
	//
	// The master-key methods above are the same operation with a fixed name and a
	// JSON shape. They stay separate because their payload has a schema the restore
	// path reads field by field, where an artefact is opaque bytes.
	BackupArtifact(ctx context.Context, clusterID, name, payload string) error
	RestoreArtifact(ctx context.Context, clusterID, name string) (string, error)
}

// ArtifactKubeconfig is the cluster's admin kubeconfig.
//
// Escrowed because it is the only credential that reaches the API server when the
// identity provider cannot be used -- it is down, the cluster is broken, or nobody
// is left who can log in (ADR-076). Day-0 generates it on a runner destroyed with
// the job, so without this it exists only inside the cluster it would be used to
// repair.
const ArtifactKubeconfig = "admin-kubeconfig"

// MasterKeysBackup is what is escrowed. The field names are the ones the restore
// path reads back, so changing one is a migration rather than a rename.
type MasterKeysBackup struct {
	EncryptionKey   string    `json:"encryptionKey"`
	AuthSecret      string    `json:"authSecret"`
	CreatedAt       time.Time `json:"createdAt"`
	ClusterID       string    `json:"clusterId"`
	Version         string    `json:"version"`
	BackupTimestamp time.Time `json:"backupTimestamp"`
}

// infisicalEscrow escrows into an Infisical instance outside this box.
//
// It speaks the Infisical API directly rather than reusing either of the platform's
// existing clients. Both are large and carry concerns an escrow has none of --
// identity creation, credential rotation, folder trees, project bootstrap -- and
// both live behind Go's internal rule in packages the other side cannot import. An
// escrow is four calls: authenticate, does-it-exist, read, write.
type infisicalEscrow struct {
	baseURL      string
	clientID     string
	clientSecret string
	projectID    string

	http  *http.Client
	token string
}

// environmentSlug is the Infisical environment the escrow writes to.
//
// Fixed rather than configurable: an escrow split across environments is one a
// restore has to guess at, and the thing being escrowed has no environment of its
// own -- it belongs to a cluster, which the path already names.
const environmentSlug = "prod"

const (
	pathLogin      = "/api/v1/auth/universal-auth/login"
	pathSecretsRaw = "/api/v3/secrets/raw/%s"
	pathFolders    = "/api/v1/folders"
)

// escrowSecretName is the single secret each cluster's master-key backup occupies.
// One name, no timestamps: a restore must find exactly one answer, and a list of
// dated backups is a decision nobody is present to make when it is needed.
const escrowSecretName = "infisical-master-keys"

// escrowPath is where a cluster's artefacts live. Keyed by cluster so one tenant's
// several boxes do not overwrite each other.
func escrowPath(clusterID string) string {
	return "/hub-operator/" + clusterID
}

// NewEscrowClient builds the escrow from the environment, or reports why it cannot.
//
// inClusterURL is this box's own Infisical, when the caller knows it. It is what
// makes this an escrow rather than a second copy in the same place: an escrow
// inside the thing it protects is unreachable exactly when it is needed, and this
// refuses that configuration rather than appearing to work. Callers with no cluster
// to compare against -- the CLI retrieving a copy -- pass "".
func NewEscrowClient(ctx context.Context, inClusterURL string) (EscrowClient, error) {
	url := strings.TrimSpace(os.Getenv("INFISICAL_ESCROW_URL"))
	clientID := strings.TrimSpace(os.Getenv("INFISICAL_ESCROW_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("INFISICAL_ESCROW_CLIENT_SECRET"))
	projectID := strings.TrimSpace(os.Getenv("INFISICAL_ESCROW_PROJECT_ID"))

	if url == "" && clientID == "" && clientSecret == "" && projectID == "" {
		return nil, fmt.Errorf("no escrow configured")
	}
	// All four or none. Reading them from one Secret means a partial set produces a
	// caller that attempts a backup on every reconcile and fails -- an escrow that
	// appears to exist and does not work, which is worse than none.
	var missing []string
	for _, kv := range []struct{ name, value string }{
		{"INFISICAL_ESCROW_URL", url},
		{"INFISICAL_ESCROW_PROJECT_ID", projectID},
		{"INFISICAL_ESCROW_CLIENT_ID", clientID},
		{"INFISICAL_ESCROW_CLIENT_SECRET", clientSecret},
	} {
		if kv.value == "" {
			missing = append(missing, kv.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the escrow is partially configured; missing %s",
			strings.Join(missing, ", "))
	}

	if inClusterURL != "" && strings.EqualFold(
		strings.TrimSuffix(url, "/"), strings.TrimSuffix(inClusterURL, "/")) {
		return nil, fmt.Errorf("the escrow URL is this cluster's own Infisical (%s), "+
			"which is not an escrow: it holds the keys that decrypt it, and it is "+
			"unreachable exactly when they are needed", url)
	}

	e := &infisicalEscrow{
		baseURL:      strings.TrimSuffix(url, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		projectID:    projectID,
		http:         &http.Client{Timeout: 30 * time.Second},
	}
	if err := e.authenticate(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

// authenticate exchanges the machine identity for a token.
func (e *infisicalEscrow) authenticate(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{
		"clientId":     e.clientID,
		"clientSecret": e.clientSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+pathLogin,
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build escrow login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach the escrow at %s: %w", e.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("the escrow refused these credentials (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("read the escrow login response: %w", err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("the escrow returned no access token")
	}
	e.token = out.AccessToken
	return nil
}

func (e *infisicalEscrow) secretURL(clusterID, name string) string {
	return fmt.Sprintf("%s%s?workspaceId=%s&environment=%s&secretPath=%s",
		e.baseURL, fmt.Sprintf(pathSecretsRaw, name), e.projectID, environmentSlug,
		escrowPath(clusterID))
}

// ensurePath creates this cluster's escrow folder.
//
// Infisical does not create a secret path on write: a secret written to a path that
// does not exist is a 404, not an implicit mkdir (secret-v2-bridge-service.ts
// findBySecretPath -> NotFoundError). The folder API does fill in missing parents of
// the path it is given, so one call creates both segments.
//
// Already-exists is the normal case -- every backup after the first -- and Infisical
// reports it as a 400, so it is success here.
func (e *infisicalEscrow) ensurePath(ctx context.Context, clusterID string) error {
	parent, name := path.Split(escrowPath(clusterID))
	body, _ := json.Marshal(map[string]string{
		"workspaceId": e.projectID,
		"environment": environmentSlug,
		"path":        strings.TrimSuffix(parent, "/"),
		"name":        name,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+pathFolders,
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build escrow folder create: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("create the escrow folder: %w", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusConflict:
		return nil
	default:
		return fmt.Errorf("the escrow refused to create %s (%d): %s",
			escrowPath(clusterID), resp.StatusCode, strings.TrimSpace(string(b)))
	}
}

// BackupArtifact writes one named artefact into this cluster's escrow.
//
// Create, then update on conflict. Infisical distinguishes the two and an escrow
// must not care: the second backup of a cluster is the normal case.
func (e *infisicalEscrow) BackupArtifact(ctx context.Context, clusterID, name, payload string) error {
	if err := e.ensurePath(ctx, clusterID); err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]string{
		"workspaceId": e.projectID,
		"environment": environmentSlug,
		"secretPath":  escrowPath(clusterID),
		"secretValue": payload,
		"type":        "shared",
	})

	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		req, err := http.NewRequestWithContext(ctx, method,
			e.baseURL+fmt.Sprintf(pathSecretsRaw, name), bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build escrow write: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.token)

		resp, err := e.http.Do(req)
		if err != nil {
			return fmt.Errorf("write the escrow: %w", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		// A create against an existing secret is not a failure; retry as an update.
		if method == http.MethodPost &&
			(resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusBadRequest) {
			continue
		}
		return fmt.Errorf("the escrow refused the write (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return fmt.Errorf("the escrow accepted neither a create nor an update for %s", name)
}

// RestoreArtifact returns one named artefact, or "" when this cluster has none.
//
// Empty and an error are different answers, and callers depend on it: empty means
// nothing was ever escrowed under that name, an error means the escrow could not be
// read -- and continuing past the second would overwrite a backup that exists.
func (e *infisicalEscrow) RestoreArtifact(ctx context.Context, clusterID, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		e.secretURL(clusterID, name), nil)
	if err != nil {
		return "", fmt.Errorf("build escrow read: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("read the escrow: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("the escrow refused the read (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out struct {
		Secret struct {
			SecretValue string `json:"secretValue"`
		} `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("read the escrow response: %w", err)
	}
	return out.Secret.SecretValue, nil
}

// BackupMasterKeys writes this cluster's Infisical master keys into the escrow.
func (e *infisicalEscrow) BackupMasterKeys(ctx context.Context, clusterID string, keys interface{}) error {
	payload, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("marshal escrow payload: %w", err)
	}
	return e.BackupArtifact(ctx, clusterID, escrowSecretName, string(payload))
}

// RestoreMasterKeys returns this cluster's escrowed keys, or nil when there are
// none.
func (e *infisicalEscrow) RestoreMasterKeys(ctx context.Context, clusterID string) (interface{}, error) {
	raw, err := e.RestoreArtifact(ctx, clusterID, escrowSecretName)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("the escrowed keys for %s are not readable as a backup: %w. "+
			"Generating new keys over them would make the existing store permanently "+
			"undecryptable, so this fails instead", clusterID, err)
	}
	return out, nil
}

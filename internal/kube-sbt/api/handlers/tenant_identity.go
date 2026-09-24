package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// TenantIdentityHandler provisions a tenant's identity resources at the issuer.
//
// ADR-041 assigns tenant identity lifecycle to this service and tenant
// environment provisioning to the Hub Operator, so the operator ORCHESTRATES
// and calls this; it does not create identity resources itself. That split is
// why this is an endpoint rather than a loop reading tenant records — the tenant
// registry belongs to the operator, and reading it from here would be this
// service reaching into another component's domain.
type TenantIdentityHandler struct {
	provisioner interfaces.ITenantIdentityProvisioner
	publish     func(tenantID, clientID string) error
}

func NewTenantIdentityHandler(p interfaces.ITenantIdentityProvisioner, publish func(tenantID, clientID string) error) *TenantIdentityHandler {
	return &TenantIdentityHandler{provisioner: p, publish: publish}
}

type ensureTenantIdentityRequest struct {
	// RedirectURIs are registered on the tenant's OAuth application. The issuer
	// matches them exactly, with no wildcards, so an environment's hostname must
	// appear here or its login fails at the authorization endpoint before any
	// credential is entered — which reads as a broken login page rather than a
	// missing registration.
	RedirectURIs   []string `json:"redirectUris"`
	PostLogoutURIs []string `json:"postLogoutUris"`
	// KnownOrgID is the IMMUTABLE binding from the tenant's record (ADR-088).
	// Empty means unbound: the service may then create an organisation if none
	// exists, and must REFUSE if one does, rather than adopting it by name.
	KnownOrgID string `json:"knownOrgId"`
	// OwnerEmail is granted administrative access, so the tenant has someone who
	// can sign in to it at all.
	OwnerEmail string `json:"ownerEmail"`
	// SelfRegistration lets anyone reaching this tenant's hostname create an
	// account in it. Scoped to this tenant's organisation by the gateway, so it
	// cannot create accounts anywhere else.
	SelfRegistration bool `json:"selfRegistration"`
	// OAuthClients are the clients this FLEET declares. Exactly this set is
	// provisioned and no more: a fleet that declares none gets none, rather than
	// a set the platform chose for it (ADR-047).
	OAuthClients []struct {
		Name          string   `json:"name"`
		Confidential  bool     `json:"confidential"`
		RedirectPaths []string `json:"redirectPaths"`
	} `json:"oauthClients"`
}

// EnsureIdentity is idempotent: it reports the tenant's identity whether it
// created it or found it. The caller reconciles, so it will be called
// repeatedly, and an "already exists" answer must be success rather than a
// conflict the caller has to interpret.
func (h *TenantIdentityHandler) EnsureIdentity(c *gin.Context) {
	tenantID := c.Param("tenantId")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenantId is required"})
		return
	}

	if h.provisioner == nil {
		// Not an error the caller can fix by retrying: the configured provider
		// has no notion of a tenant to provision. Said plainly so a reconcile
		// loop stops rather than spins.
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "the configured identity provider does not provision tenant identities",
		})
		return
	}

	var req ensureTenantIdentityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	clients := make([]models.OAuthClient, 0, len(req.OAuthClients))
	for _, oc := range req.OAuthClients {
		clients = append(clients, models.OAuthClient{
			Name:          oc.Name,
			Confidential:  oc.Confidential,
			RedirectPaths: oc.RedirectPaths,
		})
	}

	identity, err := h.provisioner.EnsureTenantIdentity(c.Request.Context(), tenantID, req.KnownOrgID, req.OwnerEmail, req.SelfRegistration, req.RedirectURIs, req.PostLogoutURIs, clients)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// The clients the fleet DECLARED, provisioned after the tenant's own
	// identity exists.
	//
	// This loop is the whole reason a fleet may declare a confidential client.
	// It was written on ControlPlane.EnsureTenantIdentity, which nothing
	// reaches: this service constructs no ControlPlane, and the route is wired
	// straight to the provider. So a fleet declaring a client got a 200, no
	// credential, and no error -- its ExternalSecret then sat in
	// SecretSyncedError naming keys nothing had ever written.
	//
	// Credentials are RETURNED, not stored. This service provisions at the
	// issuer; the caller owns where secrets live (ADR-003), which is the same
	// division OwnerPassword and OIDC_CLIENT_ID already follow.
	for _, decl := range clients {
		if !decl.Confidential || decl.Name == "" {
			continue
		}
		appName := tenantID + "-" + decl.Name
		clientID, clientSecret, cerr := h.provisioner.EnsureConfidentialClient(
			c.Request.Context(), tenantID, appName, false)
		if cerr != nil {
			// Reported, not fatal. The tenant identity itself succeeded and is
			// worth returning; a client that failed is named in Incomplete so
			// the caller learns which capability is missing rather than
			// discovering it as a workload that will not start.
			identity.Incomplete = append(identity.Incomplete,
				fmt.Sprintf("provision declared client %q: %v", decl.Name, cerr))
			continue
		}
		identity.Clients = append(identity.Clients, models.DeclaredClient{
			Name:         decl.Name,
			ClientID:     clientID,
			ClientSecret: clientSecret,
		})
	}

	// Published before the response. A caller that saw success and then found no
	// client id could not tell "not provisioned" from "provisioned, publish
	// failed", and only the second needs a retry.
	if h.publish != nil {
		if err := h.publish(tenantID, identity.ClientID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "identity provisioned but not published: " + err.Error()})
			return
		}
	}

	resp := gin.H{
		"tenantRef":  identity.TenantRef,
		"projectRef": identity.ProjectRef,
		"clientId":   identity.ClientID,
	}
	// What did not finish, when anything did not. Carried in a SUCCESSFUL
	// response on purpose: the identifiers above are real and must be published,
	// and the tenant is nonetheless not yet usable. Sending a failure status
	// instead would suppress the identifiers to report a problem with something
	// else.
	if len(identity.Incomplete) > 0 {
		resp["incomplete"] = identity.Incomplete
	}
	// Present ONLY on the call that created the owner's account. Its absence on
	// later calls is the signal that no new credential exists, so a caller that
	// persists it unconditionally would overwrite a stored password with an
	// empty value.
	if identity.OwnerPassword != "" {
		resp["ownerPassword"] = identity.OwnerPassword
	}
	if len(identity.Clients) > 0 {
		out := make([]map[string]string, 0, len(identity.Clients))
		for _, dc := range identity.Clients {
			out = append(out, map[string]string{
				"name":         dc.Name,
				"clientId":     dc.ClientID,
				"clientSecret": dc.ClientSecret,
			})
		}
		resp["clients"] = out
	}
	c.JSON(http.StatusOK, resp)
}

// PlatformAppProvisioner is implemented by providers that can create an
// application in the PLATFORM's own organisation, as distinct from a tenant's.
//
// Optional and asserted at the call site, for the same reason the tenant
// capability is: a provider with no notion of an organisation has no such
// distinction to make and would have to invent an answer.
type PlatformAppProvisioner interface {
	EnsurePlatformApp(ctx context.Context, appName string, redirectURIs, postLogoutURIs []string) (string, error)
}

// PlatformAppHandler provisions a platform-level OIDC client.
//
// The Kubernetes API server is the motivating consumer: its client id is an
// API-server FLAG, so it cannot be read at runtime the way a tenant's gateway
// reads one, and it has to exist before the flag can name it.
type PlatformAppHandler struct{ provisioner PlatformAppProvisioner }

func NewPlatformAppHandler(p PlatformAppProvisioner) *PlatformAppHandler {
	return &PlatformAppHandler{provisioner: p}
}

type ensurePlatformAppRequest struct {
	Name           string   `json:"name"`
	RedirectURIs   []string `json:"redirectUris"`
	PostLogoutURIs []string `json:"postLogoutUris"`
}

func (h *PlatformAppHandler) EnsureApp(c *gin.Context) {
	if h.provisioner == nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "the configured identity provider does not provision platform applications",
		})
		return
	}
	var req ensurePlatformAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	clientID, err := h.provisioner.EnsurePlatformApp(c.Request.Context(), req.Name, req.RedirectURIs, req.PostLogoutURIs)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": req.Name, "clientId": clientID})
}

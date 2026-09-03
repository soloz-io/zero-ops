package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
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
	// OwnerEmail is granted administrative access, so the tenant has someone who
	// can sign in to it at all.
	OwnerEmail string `json:"ownerEmail"`
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

	identity, err := h.provisioner.EnsureTenantIdentity(c.Request.Context(), tenantID, req.OwnerEmail, req.RedirectURIs, req.PostLogoutURIs)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
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
	// Present ONLY on the call that created the owner's account. Its absence on
	// later calls is the signal that no new credential exists, so a caller that
	// persists it unconditionally would overwrite a stored password with an
	// empty value.
	if identity.OwnerPassword != "" {
		resp["ownerPassword"] = identity.OwnerPassword
	}
	c.JSON(http.StatusOK, resp)
}

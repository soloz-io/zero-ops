package preflight

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/hcloud"
)

// HetznerTokenValidator validates Hetzner API token
type HetznerTokenValidator struct {
	Token string
}

func (v *HetznerTokenValidator) Validate(ctx context.Context) error {
	client := hcloud.NewClient(hcloud.WithToken(v.Token))
	
	// Use read-only operation to validate token
	_, _, err := client.Datacenter.List(ctx, hcloud.DatacenterListOpts{})
	if err != nil {
		if hcloud.IsError(err, hcloud.ErrorCodeUnauthorized) {
			return fmt.Errorf("invalid Hetzner token")
		}
		return fmt.Errorf("token validation failed: %w", err)
	}
	
	return nil
}

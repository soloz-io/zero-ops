package models

import (
	"errors"
	"strings"
)

// ValidateTenant validates a Tenant model
func ValidateTenant(t Tenant) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("tenant name is required")
	}
	if strings.TrimSpace(t.OwnerEmail) == "" {
		return errors.New("tenant owner email is required")
	}
	if strings.TrimSpace(t.Tier) == "" {
		return errors.New("tenant tier is required")
	}
	return nil
}

// ValidateUser validates a User model
func ValidateUser(u User) error {
	if strings.TrimSpace(u.Email) == "" {
		return errors.New("user email is required")
	}
	if strings.TrimSpace(u.TenantID) == "" {
		return errors.New("user tenant_id is required")
	}
	return nil
}

// ValidateTenantRegistration validates a TenantRegistration model
func ValidateTenantRegistration(r TenantRegistration) error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("registration name is required")
	}
	if strings.TrimSpace(r.Email) == "" {
		return errors.New("registration email is required")
	}
	if strings.TrimSpace(r.Tier) == "" {
		return errors.New("registration tier is required")
	}
	return nil
}

// ValidateProvisionRequest validates a ProvisionRequest model
func ValidateProvisionRequest(r ProvisionRequest) error {
	if strings.TrimSpace(r.TenantID) == "" {
		return errors.New("provision request tenant_id is required")
	}
	if strings.TrimSpace(r.Tier) == "" {
		return errors.New("provision request tier is required")
	}
	return nil
}

// ValidateTierConfig validates a TierConfig model
func ValidateTierConfig(t TierConfig) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("tier name is required")
	}
	if strings.TrimSpace(t.DisplayName) == "" {
		return errors.New("tier display_name is required")
	}
	return nil
}

// ValidateEvent validates an Event model
func ValidateEvent(e Event) error {
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("event id is required")
	}
	if strings.TrimSpace(e.DetailType) == "" {
		return errors.New("event detailType is required")
	}
	if strings.TrimSpace(e.Source) == "" {
		return errors.New("event source is required")
	}
	return nil
}

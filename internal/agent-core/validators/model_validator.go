package validators

import (
	"context"
	"fmt"
)

// ModelValidator validates model authorization based on tenant tier
type ModelValidator struct{}

// NewModelValidator creates a new model validator
func NewModelValidator() *ModelValidator {
	return &ModelValidator{}
}

// ValidateModelAuthorization checks if tenant tier allows access to requested model
func (v *ModelValidator) ValidateModelAuthorization(ctx context.Context, provider, model, tenantTier string) error {
	// Validate provider
	validProviders := map[string]bool{
		"openai":    true,
		"anthropic": true,
		"google":    true,
	}
	
	if !validProviders[provider] {
		return &ValidationError{
			Code:    "invalid_provider",
			Message: fmt.Sprintf("Invalid provider '%s'. Supported providers: openai, anthropic, google", provider),
		}
	}
	
	// Tier-based model authorization
	tierModels := map[string]map[string]bool{
		"basic": {
			"gpt-3.5-turbo":     true,
			"claude-3-haiku":    true,
			"gemini-1.5-flash":  true,
		},
		"standard": {
			"gpt-3.5-turbo":     true,
			"gpt-4":             true,
			"claude-3-haiku":    true,
			"claude-3-sonnet":   true,
			"gemini-1.5-flash":  true,
			"gemini-1.5-pro":    true,
		},
		"premium": {
			"gpt-3.5-turbo":     true,
			"gpt-4":             true,
			"gpt-4-turbo":       true,
			"claude-3-haiku":    true,
			"claude-3-sonnet":   true,
			"claude-3-opus":     true,
			"gemini-1.5-flash":  true,
			"gemini-1.5-pro":    true,
		},
		"enterprise": {
			"gpt-3.5-turbo":     true,
			"gpt-4":             true,
			"gpt-4-turbo":       true,
			"gpt-4o":            true,
			"claude-3-haiku":    true,
			"claude-3-sonnet":   true,
			"claude-3-opus":     true,
			"claude-3.5-sonnet": true,
			"gemini-1.5-flash":  true,
			"gemini-1.5-pro":    true,
			"gemini-2.0-flash":  true,
		},
	}
	
	allowedModels, tierExists := tierModels[tenantTier]
	if !tierExists {
		return &ValidationError{
			Code:    "invalid_tier",
			Message: fmt.Sprintf("Invalid tenant tier '%s'", tenantTier),
		}
	}
	
	if !allowedModels[model] {
		return &ValidationError{
			Code:    "unauthorized_model",
			Message: fmt.Sprintf("Model '%s' not authorized for tier '%s'. Upgrade tier to access this model.", model, tenantTier),
			Details: map[string]interface{}{
				"model":       model,
				"tenant_tier": tenantTier,
			},
		}
	}
	
	return nil
}

// ValidationError represents a validation error
type ValidationError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (e *ValidationError) Error() string {
	return e.Message
}

package provision

import "strings"

// unrecoverableHcloudError decides whether retrying a Hetzner API failure is
// pointless. Auth errors (401/403), validation errors (400/422) and resource
// conflicts (409) are permanent; network errors and 5xx are recoverable and should
// be retried by the reconciler on the next tick.
func unrecoverableHcloudError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range []string{"API error 400", "API error 401", "API error 403", "API error 409", "API error 422"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

// isGone returns true when a Hetzner API error indicates the resource is
// already deleted (404) or already detached (422 "volume not attached").
func isGone(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "API error 404") ||
		strings.Contains(msg, "volume not attached to a server")
}

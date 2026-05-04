package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// isConditionTrueAndUpToDate checks if a condition exists, has Status=True,
// AND has observedGeneration matching the current resource generation.
//
// This ensures that phases are re-run when the spec changes, even if the
// condition was previously True for an older generation.
//
// Use this instead of meta.IsStatusConditionTrue() for all phase gate checks.
//
// Example:
//   if !isConditionTrueAndUpToDate(hubEnv.Status.Conditions, "DatabaseRolesConfigured", hubEnv.Generation) {
//       // Run Phase 2 - database role creation
//   }
func isConditionTrueAndUpToDate(conditions []metav1.Condition, conditionType string, currentGeneration int64) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			// Condition must be True AND observedGeneration must match current generation
			return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == currentGeneration
		}
	}
	// Condition doesn't exist - not up to date
	return false
}

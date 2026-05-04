package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsConditionTrueAndUpToDate(t *testing.T) {
	tests := []struct {
		name               string
		conditions         []metav1.Condition
		conditionType      string
		currentGeneration  int64
		expectedResult     bool
	}{
		{
			name: "condition is True and observedGeneration matches",
			conditions: []metav1.Condition{
				{
					Type:               "DatabaseRolesConfigured",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 3,
				},
			},
			conditionType:     "DatabaseRolesConfigured",
			currentGeneration: 3,
			expectedResult:    true,
		},
		{
			name: "condition is True but observedGeneration is outdated",
			conditions: []metav1.Condition{
				{
					Type:               "DatabaseRolesConfigured",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 2,
				},
			},
			conditionType:     "DatabaseRolesConfigured",
			currentGeneration: 3,
			expectedResult:    false,
		},
		{
			name: "condition is False",
			conditions: []metav1.Condition{
				{
					Type:               "DatabaseRolesConfigured",
					Status:             metav1.ConditionFalse,
					ObservedGeneration: 3,
				},
			},
			conditionType:     "DatabaseRolesConfigured",
			currentGeneration: 3,
			expectedResult:    false,
		},
		{
			name:              "condition does not exist",
			conditions:        []metav1.Condition{},
			conditionType:     "DatabaseRolesConfigured",
			currentGeneration: 3,
			expectedResult:    false,
		},
		{
			name: "condition is Unknown",
			conditions: []metav1.Condition{
				{
					Type:               "DatabaseRolesConfigured",
					Status:             metav1.ConditionUnknown,
					ObservedGeneration: 3,
				},
			},
			conditionType:     "DatabaseRolesConfigured",
			currentGeneration: 3,
			expectedResult:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConditionTrueAndUpToDate(tt.conditions, tt.conditionType, tt.currentGeneration)
			if result != tt.expectedResult {
				t.Errorf("isConditionTrueAndUpToDate() = %v, want %v", result, tt.expectedResult)
			}
		})
	}
}

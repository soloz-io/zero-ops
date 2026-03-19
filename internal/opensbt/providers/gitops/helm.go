package gitops

import (
	"fmt"
	"strings"
)

// tierResources defines Kubernetes resource quotas per tier (15.5, 15.7).
var tierResources = map[string]map[string]string{
	"basic": {
		"requestsCPU":    "500m",
		"requestsMemory": "1Gi",
		"limitsCPU":      "1",
		"limitsMemory":   "2Gi",
	},
	"standard": {
		"requestsCPU":    "1",
		"requestsMemory": "2Gi",
		"limitsCPU":      "2",
		"limitsMemory":   "4Gi",
	},
	"premium": {
		"requestsCPU":    "2",
		"requestsMemory": "4Gi",
		"limitsCPU":      "4",
		"limitsMemory":   "8Gi",
	},
	"enterprise": {
		"requestsCPU":    "4",
		"requestsMemory": "8Gi",
		"limitsCPU":      "8",
		"limitsMemory":   "16Gi",
	},
}

// tierDatabaseType maps tier to database isolation strategy.
var tierDatabaseType = map[string]string{
	"basic":      "shared",
	"standard":   "shared",
	"premium":    "dedicated",
	"enterprise": "dedicated",
}

// generateHelmValues produces the tenant values.yaml content for the
// Universal Tenant Helm Chart (15.4, 15.5).
func generateHelmValues(tenantID, tier, email string, extra map[string]interface{}) []byte {
	res := tierResources[tier]
	if res == nil {
		res = tierResources["basic"]
	}
	dbType := tierDatabaseType[tier]
	if dbType == "" {
		dbType = "shared"
	}

	// Dedicated DB name for premium/enterprise
	dbName := "shared-db"
	if dbType == "dedicated" {
		dbName = fmt.Sprintf("tenant-%s-db", tenantID)
	}

	// ArgoCD agent mode: managed for basic/standard, autonomous for premium/enterprise
	agentMode := "managed"
	if tier == "premium" || tier == "enterprise" {
		agentMode = "autonomous"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("tenantId: %q\n", tenantID))
	sb.WriteString(fmt.Sprintf("tier: %q\n", tier))
	sb.WriteString(fmt.Sprintf("ownerEmail: %q\n", email))
	sb.WriteString(fmt.Sprintf("assigned: true\n"))

	// Resources (15.7)
	sb.WriteString("resources:\n")
	sb.WriteString(fmt.Sprintf("  requestsCPU: %q\n", res["requestsCPU"]))
	sb.WriteString(fmt.Sprintf("  requestsMemory: %q\n", res["requestsMemory"]))
	sb.WriteString(fmt.Sprintf("  limitsCPU: %q\n", res["limitsCPU"]))
	sb.WriteString(fmt.Sprintf("  limitsMemory: %q\n", res["limitsMemory"]))

	// Database (15.10 — Crossplane XR hint)
	sb.WriteString("database:\n")
	sb.WriteString(fmt.Sprintf("  type: %q\n", dbType))
	sb.WriteString(fmt.Sprintf("  name: %q\n", dbName))

	// ArgoCD agent (16.4, 16.5)
	sb.WriteString("argocdAgent:\n")
	sb.WriteString(fmt.Sprintf("  enabled: true\n"))
	sb.WriteString(fmt.Sprintf("  mode: %q\n", agentMode))

	// Extra values from caller
	for k, v := range extra {
		sb.WriteString(fmt.Sprintf("%s: %v\n", k, v))
	}

	return []byte(sb.String())
}

// generateWarmSlotValues produces values.yaml for an unassigned warm pool slot (15.11).
func generateWarmSlotValues(slotID, tier string) []byte {
	res := tierResources[tier]
	if res == nil {
		res = tierResources["basic"]
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("tenantId: %q\n", slotID))
	sb.WriteString(fmt.Sprintf("tier: %q\n", tier))
	sb.WriteString("assigned: false\n")
	sb.WriteString("resources:\n")
	sb.WriteString(fmt.Sprintf("  requestsCPU: %q\n", res["requestsCPU"]))
	sb.WriteString(fmt.Sprintf("  requestsMemory: %q\n", res["requestsMemory"]))
	sb.WriteString(fmt.Sprintf("  limitsCPU: %q\n", res["limitsCPU"]))
	sb.WriteString(fmt.Sprintf("  limitsMemory: %q\n", res["limitsMemory"]))
	sb.WriteString("database:\n")
	sb.WriteString(fmt.Sprintf("  type: %q\n", tierDatabaseType[tier]))
	sb.WriteString("argocdAgent:\n")
	sb.WriteString("  enabled: false\n")
	return []byte(sb.String())
}

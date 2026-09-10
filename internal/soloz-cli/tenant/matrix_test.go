package tenant

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The CLI carries its own copy of the supported matrix so it can refuse a
// combination before a repository exists. A copy is only safe while something
// fails when the two disagree -- otherwise the CLI accepts what the chart will
// refuse, which is the failure this gate was added to prevent, restored by drift.
func TestScaffoldMatrixMatchesTheChart(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root,
		"manifests", "argocd", "environment-manager", "values.yaml"))
	if err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	var values struct {
		SupportedMatrix map[string][]string `yaml:"supportedMatrix"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("parse chart values: %v", err)
	}
	if len(values.SupportedMatrix) == 0 {
		t.Fatal("the chart declares no supportedMatrix; this test would pass vacuously")
	}

	for env, chartProviders := range values.SupportedMatrix {
		cliProviders, ok := supportedMatrix[env]
		if !ok {
			t.Errorf("the chart supports environment %q and the CLI does not; "+
				"scaffolding would refuse a combination the platform ships", env)
			continue
		}
		for _, p := range chartProviders {
			if !contains(cliProviders, p) {
				t.Errorf("the chart supports %s+%s and the CLI does not", env, p)
			}
		}
		for _, p := range cliProviders {
			if !contains(chartProviders, p) {
				t.Errorf("the CLI accepts %s+%s and the chart refuses it: scaffolding "+
					"would create a repository whose bundle cannot render", env, p)
			}
		}
	}
	for env := range supportedMatrix {
		if _, ok := values.SupportedMatrix[env]; !ok {
			t.Errorf("the CLI accepts environment %q and the chart does not", env)
		}
	}
}

// Every supported combination must have the spoke-pool source the chart's refusal
// message says is the reason it is supported at all.
func TestEverySupportedCombinationHasASpokePoolSource(t *testing.T) {
	root := repoRootForTest(t)
	for env, providers := range supportedMatrix {
		for _, p := range providers {
			dir := filepath.Join(root, "manifests", "spoke", "spoke-pools", env, p)
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("%s+%s is supported but has no spoke-pool source at %s: %v",
					env, p, dir, err)
			}
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

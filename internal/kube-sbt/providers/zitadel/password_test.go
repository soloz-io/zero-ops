package zitadel

import (
	"strings"
	"testing"
)

// A pooled random draw satisfies a complexity policy only probably, and the
// failure would be a rejected user creation on some tenants and not others.
func TestInitialPasswordAlwaysSatisfiesComplexity(t *testing.T) {
	for i := 0; i < 500; i++ {
		pw, err := generateInitialPassword()
		if err != nil {
			t.Fatalf("generateInitialPassword: %v", err)
		}
		if len(pw) < 12 {
			t.Fatalf("password %q is too short", pw)
		}
		var hasLower, hasUpper, hasDigit, hasSymbol bool
		for _, c := range pw {
			switch {
			case c >= 'a' && c <= 'z':
				hasLower = true
			case c >= 'A' && c <= 'Z':
				hasUpper = true
			case c >= '0' && c <= '9':
				hasDigit = true
			default:
				hasSymbol = true
			}
		}
		if !hasLower || !hasUpper || !hasDigit || !hasSymbol {
			t.Fatalf("password %q missing a required class (lower=%v upper=%v digit=%v symbol=%v)",
				pw, hasLower, hasUpper, hasDigit, hasSymbol)
		}
	}
}

// This value travels through JSON, a secret store and a terminal. A password
// that breaks one of those is a support incident, not a security improvement.
func TestInitialPasswordAvoidsCharactersThatBreakTransport(t *testing.T) {
	for i := 0; i < 200; i++ {
		pw, err := generateInitialPassword()
		if err != nil {
			t.Fatalf("generateInitialPassword: %v", err)
		}
		if strings.ContainsAny(pw, "\"'`\\$!&|;<>() \t\n") {
			t.Fatalf("password %q contains a character that breaks quoting or shells", pw)
		}
	}
}

func TestInitialPasswordsDiffer(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		pw, _ := generateInitialPassword()
		if seen[pw] {
			t.Fatalf("generated a duplicate password: %q", pw)
		}
		seen[pw] = true
	}
}

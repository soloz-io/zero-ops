package tenant

import "testing"

func TestHubDomainByEnvironment(t *testing.T) {
	for _, c := range []struct{ env, domain, want string }{
		{"dev", "acme.example", "dev.acme.example"},
		{"stg", "acme.example", "stg.acme.example"},
		{"prod", "acme.example", "acme.example"},
		{"", "acme.example", "acme.example"},
		{"dev", "acme.example.", "dev.acme.example"},
	} {
		if got := hubDomain(c.env, c.domain); got != c.want {
			t.Errorf("hubDomain(%q,%q) = %q, want %q", c.env, c.domain, got, c.want)
		}
	}
}

package infisical

import (
	"strings"
	"testing"
)

func TestResolveInfisicalPath(t *testing.T) {
	cases := []struct {
		path, tenant, want string
		wantErr            bool
	}{
		{"", "", "/", false},
		{"", "acme", "/", false},
		{"/" + TenantPlaceholder + "-mgmt", "acme", "/acme-mgmt", false},
		// Never fall back to "/" for an owner's credential: its consumer reads the folder.
		{"/" + TenantPlaceholder + "-mgmt", "", "", true},
		{"/fixed", "", "/fixed", false},
	}
	for _, c := range cases {
		got, err := resolveInfisicalPath(c.path, c.tenant)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("resolveInfisicalPath(%q, %q) = %q, %v; want %q, err=%v", c.path, c.tenant, got, err, c.want, c.wantErr)
		}
	}
}

func TestApplyTransformURLPassword(t *testing.T) {
	got, err := applyTransform(TransformURLPassword, []byte("smtps://resend:re_secret_123@smtp.resend.com:465\n"))
	if err != nil || string(got) != "re_secret_123" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"re_just_a_key", "smtps://smtp.resend.com:465", "smtps://resend@smtp.resend.com:465", "smtps://resend:@smtp.resend.com"} {
		if _, err := applyTransform(TransformURLPassword, []byte(bad)); err == nil {
			t.Errorf("%q: expected an error", bad)
		} else if strings.Contains(err.Error(), "re_") {
			t.Errorf("%q: error leaks the value: %v", bad, err)
		}
	}
	if got, _ := applyTransform(TransformNone, []byte("x")); string(got) != "x" {
		t.Errorf("TransformNone changed the value")
	}
	if _, err := applyTransform("rot13", []byte("x")); err == nil {
		t.Errorf("unknown transform accepted")
	}
}

func TestSMTPMappingTargetsOwnersFolder(t *testing.T) {
	for _, m := range CLISecretMappings {
		if m.InfisicalKey == "zitadel_smtp_password" {
			p, err := resolveInfisicalPath(m.InfisicalPath, "acme")
			if err != nil || p != "/acme-mgmt" || m.Transform != TransformURLPassword {
				t.Fatalf("zitadel_smtp_password mapping: path=%q err=%v transform=%q", p, err, m.Transform)
			}
			return
		}
	}
	t.Fatal("no CLISecretMapping for zitadel_smtp_password")
}

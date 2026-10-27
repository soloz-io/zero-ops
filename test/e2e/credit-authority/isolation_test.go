package creditauthority_test

// The gateway's refusals (ADR-109 §4, §7), asked of a deployed Credit Authority.
//
// Besides CREDITS_URL, CREDITS_LEDGER and CREDITS_TOKEN (the coordinator's
// machine token), each of these tokens, when set, must be refused everywhere:
//
//	CREDITS_ID_TOKEN            an ID token from a real login
//	CREDITS_OTHER_APP_TOKEN     another application's machine token, for this audience
//	CREDITS_OTHER_TENANT_TOKEN  a token carrying another tenant's claim
//	CREDITS_EXPIRED_TOKEN       an expired token of the coordinator

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func (a *authority) raw(t *testing.T, method, path, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, a.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	// The raw path is what a smuggling attempt depends on; never cleaned here.
	req.URL.Opaque = path
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func refused(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound
}

// Each token that is not the coordinator's, on an operation the coordinator may perform.
func TestOnlyTheCoordinatorsTokenIsAdmitted(t *testing.T) {
	a := setup(t)
	read := fmt.Sprintf("/v2/%s/accounts", a.ledger)
	if s := a.raw(t, http.MethodGet, read, a.token, ""); s != http.StatusOK {
		t.Fatalf("the coordinator's token on %s answered %d; the fixture is wrong", read, s)
	}
	if s := a.raw(t, http.MethodGet, read, "", ""); !refused(s) {
		t.Errorf("no token: answered %d", s)
	}
	for _, name := range []string{"CREDITS_ID_TOKEN", "CREDITS_OTHER_APP_TOKEN", "CREDITS_OTHER_TENANT_TOKEN", "CREDITS_EXPIRED_TOKEN"} {
		token := os.Getenv(name)
		if token == "" {
			t.Logf("%s not set; not asked", name)
			continue
		}
		if s := a.raw(t, http.MethodGet, read, token, ""); !refused(s) {
			t.Errorf("%s: answered %d", name, s)
		}
	}
}

// Everything but the four operations, with the coordinator's own token.
func TestOnlyTheFourOperationsAreReachable(t *testing.T) {
	a := setup(t)
	l := a.ledger
	for _, c := range []struct{ method, path, body string }{
		// System routes.
		{http.MethodDelete, "/_/buckets/" + l, ""},
		{http.MethodPost, "/_/buckets/" + l + "/restore", ""},
		{http.MethodGet, "/_/exporters", ""},
		{http.MethodGet, "/_info", ""},
		{http.MethodGet, "/v2/_info", ""},
		// Ledgers and their metadata.
		{http.MethodGet, "/v2/", ""},
		{http.MethodPost, "/v2/another", `{}`},
		{http.MethodPut, "/v2/" + l + "/metadata", `{"k":"v"}`},
		{http.MethodGet, "/v2/" + l + "/_info", ""},
		{http.MethodGet, "/v2/" + l + "/stats", ""},
		// Bulk, logs, schemas, pipelines, queries.
		{http.MethodPost, "/v2/" + l + "/_bulk", `[]`},
		{http.MethodGet, "/v2/" + l + "/logs", ""},
		{http.MethodPost, "/v2/" + l + "/logs/import", ""},
		{http.MethodPost, "/v2/" + l + "/logs/export", ""},
		{http.MethodPost, "/v2/" + l + "/schemas/v1", `{}`},
		{http.MethodGet, "/v2/" + l + "/pipelines", ""},
		{http.MethodPost, "/v2/" + l + "/queries/x/run", `{}`},
		// Revert and metadata writes.
		{http.MethodPost, "/v2/" + l + "/transactions/1/revert", ""},
		{http.MethodPost, "/v2/" + l + "/transactions/1/metadata", `{"k":"v"}`},
		{http.MethodDelete, "/v2/" + l + "/transactions/1/metadata/k", ""},
		{http.MethodPost, "/v2/" + l + "/accounts/world/metadata", `{"k":"v"}`},
		// The v1 API.
		{http.MethodGet, "/" + l + "/transactions", ""},
		{http.MethodPost, "/" + l + "/transactions", `{}`},
		// Another ledger's name in an allowed path.
		{http.MethodGet, "/v2/another/accounts", ""},
		// Path tricks reaching a refused route through an allowed prefix.
		{http.MethodGet, "/v2/" + l + "/accounts/../../_info", ""},
		{http.MethodGet, "/v2/" + l + "/accounts/%2e%2e/%2e%2e/_info", ""},
		{http.MethodGet, "/v2/" + l + "/accounts/..%2f..%2f_info", ""},
		{http.MethodGet, "/v2/" + l + "//accounts", ""},
		{http.MethodGet, "/v2/" + l + "/transactions/1/../../logs", ""},
		{http.MethodPost, "/v2/" + l + "/transactions/", `{"postings":[]}`},
	} {
		if s := a.raw(t, c.method, c.path, a.token, c.body); !refused(s) {
			t.Errorf("%s %s: answered %d", c.method, c.path, s)
		}
	}
}

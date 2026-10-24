1. rules-token and read-token (Cloud access policies)

1. Sign in at grafana.com → My Account. Go to Security → Access Policies. In your stack's Grafana this is also under Administration → Users and access → Cloud access policies.
2. Create access policy for rules-token:
   - Name: zero-ops-rules
   - Realm: your stack, greenimpala2036
   - Scopes: rules: Read and rules: Write
   - Create it, then Add token (name it, set no expiry or one you'll rotate), and copy the token.
3. Create access policy for read-token:
   - Name: zero-ops-validate
   - Realm: greenimpala2036
   - Scopes: metrics: Read and logs: Read
   - Add token, and copy it.

Keep using the user IDs you already have (prometheus-user, loki-user). Only the tokens are new.

2. dashboards-token (a service account in your stack)

1. Open https://greenimpala2036.grafana.net → Administration → Users and access → Service accounts.
2. Add service account: name zero-ops-dashboards, role Editor.
3. On that account, choose Add service account token, generate it, and copy it.

3. Put them in place

Write each token to a file with no trailing newline, in zero-ops/k8-secrets/grafana-cloud/:

cd zero-ops/k8-secrets/grafana-cloud
printf '%s' '<rules token>'      > rules-token
printf '%s' '<dashboards token>' > dashboards-token
printf '%s' '<read token>'       > read-token

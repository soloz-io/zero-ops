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


Alert routing is built and checked locally (uncommitted). Before it can deliver anything, you need to supply a few values and credentials. Nothing has been sent to the stack yet.

What it does (ADR-078 addendum 4):
- Rendering: the platform builds the stack's Alertmanager config from Git and your box values.
- Delivery: a CronJob on the hub loads it every 10 minutes with pinned mimirtool 3.2.3. That corrects any change, rotated credential or UI edit within one interval. The stack rejects an invalid config and keeps its current one, so a bad change can't stop delivery.
- Routes (the platform owns the tree; every alert is grouped by cluster and alertname):

| Alerts                                                         | Go to                                                                                                  |
|----------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| PlatformHeartbeat                                              | Your IRM heartbeat, every 50s. If it stops arriving, IRM pages you, which catches a broken alert path. |
| critical / warning (CNPG backups, telemetry silent, memory, …) | Email + IRM                                                                                            |
| Anything else                                                  | Email                                                                                                  |

- Email reuses the box's existing mail account (the Resend SMTP the identity provider uses), sending from no-reply@dev.nutgraf.in. The stack's Alertmanager has no SMTP of its own.

Checked:
- The environment-manager render refuses to proceed without the alerting values, naming the missing one.
- The component packages, and the rendered CronJob carries the right per-box values.
- The render script keeps quotes and special characters in secrets intact, and fails on an empty one.
- Producers: the Go packages build and the tenant tests pass; the CLI copies are re-synced.
- Preflight: rendering, image pinning, 030, 078, the credential-producer check, and the architecture checker all pass.

Not checked: the actual load into your stack. That waits on the values below and is the TM's first check after release.

What you need to provide

1. From your stack (grafana.com → your stack → Alertmanager → Details): the Alertmanager URL and instance ID. Put them in nutgraf-gitops/registry/clusters/nutgraf-hub/values.yaml:
alerting:
  alertmanagerUrl: https://alertmanager-<region>.grafana.net
  alertmanagerId: "<instance id>"
  emailTo:
    - you@example.com

2. Three credentials, each saved to a file in zero-ops/k8-secrets/grafana-cloud/ (no trailing newline):

┌──────────────────────┬────────────────────────────────────────────────────────────────────────────────────────────────┐
│         File         │                                        Where to get it                                         │
├──────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────┤
│ alerts-token         │ Access Policies: a new policy with alerts: Read + Write, realm greenimpala2036, then Add token │
├──────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────┤
│ irm-alertmanager-url │ IRM → Integrations → New integration → Alertmanager. Copy its URL.                             │
├──────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────┤
│ irm-heartbeat-url    │ The same integration → Heartbeat. Copy the heartbeat URL and set the interval, e.g. 5 minutes. │
└──────────────────────┴────────────────────────────────────────────────────────────────────────────────────────────────┘

Then seed them:
! INFISICAL_BASE_URL=https://infisical.dev.nutgraf.in KUBECONFIG=../nutgraf-gitops/k8-secrets/kubeconfig/nutgraf-hub.kubeconfig scripts/secret/seed-external-secrets.sh nutgraf-01 --dry-run
It should list the three new keys going to /. Then run it without --dry-run.

3. Commit and push both repos, then run release.sh.

Tell me the Alertmanager URL and instance ID if you'd like me to put them in the gitops values file. I'll write the TM brief once the release is ready.

Sources:
- Configure Alertmanagers using mimirtool (https://grafana.com/docs/grafana-cloud/alerting-and-irm/alerting/set-up/configure-alertmanager/add-alertmanager-using-mimirtool/)
- Alertmanager integration for Grafana IRM (https://grafana.com/docs/grafana-cloud/alerting-and-irm/irm/configure/integrations/integration-reference/oncall/alertmanager/)
- Configure Alertmanagers (https://grafana.com/docs/grafana-cloud/observe-and-act/alert-and-measure-reliability/alerting/set-up/configure-alertmanager/)


The stack's Alertmanager is at https://alertmanager-prod-ap-south-1.grafana.net: that host exists and asks for credentials. It refuses the metrics instance ID with the new token, so the Alertmanager has its own instance ID, which only your stack details show. (The …prod-43… variant doesn't exist.)

I need two values from you:
1. The Alertmanager instance ID. It's at grafana.com → My Account → your stack greenimpala2036 → Alertmanager → Details (labelled User or Instance ID).
2. The email address(es) for platform alerts.

Send them and I'll set this in nutgraf-gitops/registry/clusters/nutgraf-hub/values.yaml, then confirm the alerts token works against it with a read-only call:
alerting:
  alertmanagerUrl: https://alertmanager-prod-ap-south-1.grafana.net
  alertmanagerId: "<instance id>"
  emailTo:
    - <address>

The credentials are already in place. After that, commit and push both repos and release, and I'll write the TM brief.
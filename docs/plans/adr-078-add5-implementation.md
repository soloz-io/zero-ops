# ADR-078 addendum 5 / ADR-083 addendum 3 / ADR-077 addendum 5 — implementation tracking

Accepted 2026-10-28. Implementation started 2026-10-28. Base for restores: the
parent of `bfbcd0d3` (the commit that removed the in-box stack).

Decisions taken during implementation (not in the ADR text):
- Loki buckets: **hub-operator ensures** `<cluster>-logs` for the hub and every
  registered spoke, with the S3 credentials it already holds (user, 2026-10-28).
- Grafana login: OIDC client registered with the box issuer, as Headlamp's is.
- Spoke query exposure (external VMAuth route, JWT viewer contract, spoke
  datasource pairs) is NOT built: ADR-083 2026-09-21 ListenerSet block stands.
  The hub's Grafana queries the hub's own stores; spokes store, alert and
  heartbeat locally.
- Versions: VM operator chart 0.68.1 (operator v0.75.0); Loki chart 7.5.0 with
  image pinned to **3.7.9** — `deletion_marker_object_store_prefix` first appears
  in v3.7 (`pkg/compactor/config.go`; absent at v3.6.16).
- Profiles: dev = one replica of each; staging/production = HA counts (§9).

## Phases (each committed locally, one TM brief at the end)

1. VictoriaMetrics: operator (converter ON, owner references ON), VMSingle a/b,
   VMAuth (in-cluster service route), VMUser (service), VMAlert platform/tenant,
   VMAlertmanager (add.4 route tree, IRM, SMTP), dedup/eval 30s — hub and spoke.
2. Alloy: remote_write to both VMSingles; receiver for vmalert; cluster agent
   StatefulSet + clustering; remove mimir.rules.kubernetes and cloud auth;
   GenAI content attribute deletion on OTLP; rule groups at 30s.
3. Loki: chart (SingleBinary), S3 per cluster, compactor ring + object-store
   markers, gateway/caches/canary off; Alloy loki.write to it; hub-operator
   bucket ensure.
4. Grafana on hub: in-cluster Grafana (×2 staging/prod), grafana DB on
   platform-db, OIDC client, HTTPRoute, hub datasources (30s/60s timeInterval),
   Multi-Cluster dashboard composition, datasource-health rule.
5. Retire Grafana Cloud: alert-routing CronJob, cloud ExternalSecrets, cloud
   tokens across CLI/operator/workflows/seed/preflight, remove-in-box script,
   078-collection-floor.sh to query in-cluster, components.yaml + ADR-078 add.5
   architecture block, ADR-077 never-egress list in NetworkPolicy.

## Progress

- [x] 1  - [x] 2  - [x] 3  - [x] 4  - [x] 5  - [x] TM brief (in chat, 2026-10-28)

### Notes from phases 1–2
- VMSingle ×2 in EVERY environment (one write topology); dev only relaxes
  spreading (`ScheduleAnyway`). ADR §9 wording to match.
- Service tokens: ESO `Password` generator in-cluster (not Infisical) — ADR §7
  wording to match.
- `templated-fields.py` gained `type: int` for per-environment replica counts.
- NetworkPolicy in platform-observability is namespace-wide allow; VMAuth access
  is bounded by namespace + token, not per-pod policy.
- Tenant rule-interval admission: spoke Kyverno `tenant-rule-interval`.
- Seed/preflight key updates (ALERT_SMTP_PASSWORD, per-cell heartbeat URL,
  removal of cloud tokens) are in phase 5.

### Notes from phase 3
- Loki 7.5.0 chart, image 3.7.9, vendored to hub `loki/` and spoke `infra/loki/`.
- hub-operator `internal/objectstore` + `ensureLogsBucket` (SpokePool:
  `<spoke>-logs`; HubEnvironment: `<spec.clusterName>-logs`). Needs
  `minio-go v7.3.0` in the root go.mod: **dependency resolution handed to the
  TM** (`go get github.com/minio/minio-go/v7@v7.3.0 && go mod tidy`); not
  compiled here.

### Status at usage-limit stop (2026-10-28) — NOTHING COMMITTED
Done (uncommitted): phases 1–3; phase 4 Grafana CR + DB role/database/ES +
VMUser/token + datasources + HTTPRoute + Gateway listener + port-3000 policy +
dashboard selector rename (render.py + 14 files) + Grafana scrape + datasource
rule; removal of grafanaCloudStackUrl / alertmanagerUrl/Id; templated-fields.py
`type: int` and forced block style for multi-line templates. Hub and spoke
components package and render for dev and prod (verified with helm).
Decision: Grafana sign-in = operator admin secret until the §7 viewer route.
`HubEnvironment.spec.oauth.clients` is consumed by nothing.

Remaining:
- `go get github.com/minio/minio-go/v7@v7.3.0 && go mod tidy` (root module);
  build/test hub-operator. Until then the pre-commit hub-operator build fails.
- Phase 5: seed script (ALERT_SMTP_PASSWORD is operator-produced; per-cell
  `GRAFANA_IRM_HEARTBEAT_URL`), preflight 95 EXTERNAL set, remove Grafana Cloud
  tokens from CLI/handover/workflows/hub-bootstrap/secrets_platform/tests,
  078-collection-floor.sh to query in-cluster, delete remove-in-box-stack.sh,
  components.yaml capability note, platform-dashboards README, ADR-078 add.5
  §7/§9 wording (ESO generator tokens; VMSingle×2 in every env), embedded
  assets regeneration (`scripts/package/embed-platform-assets.sh`), tenant
  repo values cleanup (grafanaCloudStackUrl, alerting.alertmanager*).
- Multi-Cluster dashboard per-box composition waits on spoke datasources.
- TM brief.

### Completed 2026-10-28 — commit 6e4cffbd (local; push pending)
All five phases implemented; pre-commit hooks green. Waiting on ListenerSet:
spoke query route and viewer OIDC, spoke datasource pairs, Multi-Cluster
per-box composition. Post-release: tenant repo values cleanup, obsolete
GRAFANA_CLOUD_* keys in Infisical, per-cell heartbeat seeding.

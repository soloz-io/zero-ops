
## Legacy comamnds

./scripts/hub-bootstrap.sh dev --teardown --yes

------------------------------

Sequenced (default) — boundaries activated one at a time in phase order:

./scripts/hub-bootstrap.sh \
  --name hub-hybrid-dev \
  --provider hybrid \
  --region hel1 \
  --environment dev \
  --gating sequenced \
  --spoke spoke-pool-hybrid-dev-01 \
  --home-worker-enabled \
  --tailnet-name taila4c44b.ts.net \
  --bundle-version 0.1.3

---------------------------------

Converged (ADR-055) — all boundaries reconcile at once, red-then-green:

./scripts/hub-bootstrap.sh \
  --name hub-hybrid-dev \
  --provider hybrid \
  --region hel1 \
  --environment dev \
  --gating converged \
  --spoke spoke-pool-hybrid-dev-01 \
  --home-worker-enabled \
  --tailnet-name taila4c44b.ts.net

`--gating` defaults to `sequenced`, so omitting it gives the flow this script has
always run. Mode and environment are independent — any environment can be
created in either mode. Converged trades ordering guarantees for creation speed:
every Application is created at once and retries until its dependencies exist, so
a failure points at an Application rather than at a named phase.


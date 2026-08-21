
# Hub boostratp

## Cluster Teardown

./scripts/hub-bootstrap.sh dev --teardown --yes

## Fresh Bootstrap

./scripts/hub-bootstrap.sh \
  --name hub-hybrid-dev \
  --provider hybrid \
  --region hel1 \
  --environment dev \
  --spoke spoke-pool-hybrid-dev-01 \
  --home-worker-enabled \
  --tailnet-name taila4c44b.ts.net

# Provision workers

## Dell Hub VM
./scripts/hybrid/provision-flatcar-worker.sh --node 1

## Dell Spoke VM
./scripts/hybrid/provision-flatcar-worker.sh --node 2
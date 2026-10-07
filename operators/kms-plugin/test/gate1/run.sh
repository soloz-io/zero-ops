#!/usr/bin/env bash
# Gate 1, plumbing half: a real kubelet, a real API server, a real static pod, a real
# unix socket, real etcd, and a real restart — against a stub key store.
#
# WHAT THIS PROVES AND WHAT IT DOES NOT.
#
# Every component here is the production one except the key store. Gate 1 proper
# (docs/runbooks/kms-v2-gate-1-integration.md) needs an Infisical KMS key and a
# machine identity that only the operator can create; until those exist, the half that
# unit tests cannot reach is still worth running — and it is the half the DEK-cache
# defect hid in.
#
# Not proven here: authentication against Infisical, its rate limits, its latency, its
# error shapes, and CAPI delivery of these files.
#
# Disposable: it creates a kind cluster named kms-gate1 and deletes it at the end.
set -euo pipefail

cd "$(dirname "$0")"

CLUSTER=kms-gate1
CRYPTO_KEY="projects/gate1/locations/global/keyRings/gate1/cryptoKeys/gate1-key"
STUB=kms-gate1-keystore
IMAGE=kms-plugin:gate1
PASS=0
FAIL=0


say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }

# KEEP=1 LEAVES THE ENVIRONMENT STANDING, and a failing run says so.
#
# Without this the trap deletes the cluster on the way out, including when a step
# failed -- which destroys the only copy of the evidence needed to diagnose it. A
# disposable environment should be disposed of on SUCCESS and kept on failure.
KEEP="${KEEP:-}"
for arg in "$@"; do
  [ "$arg" = "--retain" ] && KEEP=1
done

cleanup() {
  local rc=$?
  if [ -n "$KEEP" ] || { [ "$rc" -ne 0 ] && [ "$FAIL" -gt 0 ]; }; then
    say "KEEPING the environment for inspection"
    printf '  cluster:     kubectl --context kind-%s get pods -A\n' "$CLUSTER"
    printf '  plugin log:  docker exec %s-control-plane crictl logs $(docker exec %s-control-plane crictl ps -a --name kms-plugin -q | head -1)\n' "$CLUSTER" "$CLUSTER"
    printf '  key store:   docker logs %s\n' "$STUB"
    printf '  node files:  docker exec %s-control-plane ls -l /etc/kubernetes/enc /etc/kubernetes/kms /var/run/kms\n' "$CLUSTER"
    printf '\n  then tear down with: kind delete cluster --name %s && docker rm -f %s\n' "$CLUSTER" "$STUB"
    return
  fi
  say "teardown"
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  docker rm -f "$STUB" >/dev/null 2>&1 || true
  rm -f static-pod.rendered.yaml credentials.json kind.rendered.yaml
}
trap cleanup EXIT

# ─────────────────────────────────────────────────────────────────────────────
say "1. build the plugin image"
# Built for the host architecture, because kind runs the node as a local container.
# The published image is amd64; this Dockerfile cross-compiles, so both work from one
# definition.
docker build -q -t "$IMAGE" ../.. >/dev/null
ok "image built"

say "2. start the stub key store"
docker network create kind >/dev/null 2>&1 || true
# Built inside a golang container so the host needs no toolchain beyond docker, and so
# the stub cannot accidentally be the host's binary.
docker rm -f "$STUB" >/dev/null 2>&1 || true
# THE HOST MODULE CACHE IS MOUNTED, and that is not an optimisation.
#
# The stub now imports cloud.google.com/go/kms, so a fresh container would download the
# Google SDK and its transitive tree before it could serve. On a cold cache that
# exceeds the 120s wait below, and the gate would fail at step 2 with "stub did not
# start" -- a timeout reported as a defect. The host has already downloaded these
# (go.sum pins them), so the cache is shared read-only.
GOMODCACHE_HOST="$(go env GOMODCACHE 2>/dev/null || echo "$HOME/go/pkg/mod")"
docker run -d --name "$STUB" --network kind \
  -v "$PWD/../..":/src -w /src \
  -v "$GOMODCACHE_HOST":/go/pkg/mod:ro \
  -e GOFLAGS=-mod=mod \
  golang:1.26 go run ./test/gate1/keystore-stub \
    --addr=:8200 --admin-addr=:8201 --crypto-key="$CRYPTO_KEY" >/dev/null
# 90 x 2s: compiling the Google SDK on first run takes longer than serving it.
for _ in $(seq 1 90); do
  docker logs "$STUB" 2>&1 | grep -q "serving Cloud KMS gRPC" && break
  sleep 2
done
docker logs "$STUB" 2>&1 | grep -q "serving Cloud KMS gRPC" \
  && ok "stub serving Cloud KMS gRPC for $CRYPTO_KEY" || { bad "stub did not start"; docker logs "$STUB" 2>&1 | tail -5; exit 1; }

say "3. render the production static pod for this run"
# THE PRODUCTION MANIFEST IS THE SOURCE. Only the three substitution markers and the
# image reference change — the securityContext, the resources, the mounts and the
# initContainer are whatever deploy/static-pod.yaml says, so this run exercises them.
python3 - <<PY
import re, pathlib
src = pathlib.Path("../../deploy/static-pod.yaml").read_text()
out = src
# CLUSTER_NAME_VALUE, not CLUSTER_NAME. A placeholder spelled the same as the env
# var's NAME got substituted on both, producing `- name: kms-gate1` -- so the plugin
# read an unset CLUSTER_NAME and refused to start. Caught by dry-rendering the pod
# before running the gate, which is why that step exists.
# KMS_INSECURE_TEST_ENDPOINT is injected for THIS RUN ONLY. The plugin logs a WARNING
# on every start when it is set, deliberately: the variable disables authentication and
# points the client away from Cloud KMS, so it must never be set quietly.
#
# ANCHORED ON THE KMS_CRYPTO_KEY ENTRY, and the substitution is ASSERTED. It used to be
# anchored on a CLUSTER_NAME env entry; that entry was removed when the plugin stopped
# composing its own identifier and read the cluster only as a key-id suffix. A
# str.replace that matches nothing returns the string unchanged, so this would have
# rendered a pod with no test endpoint, the plugin would have dialled real Cloud KMS,
# and the gate would have failed somewhere far away from the cause.
anchor = "        - name: KMS_CRYPTO_KEY\n          value: KMS_CRYPTO_KEY_VALUE"
if anchor not in out:
    raise SystemExit(
        "deploy/static-pod.yaml no longer carries the KMS_CRYPTO_KEY env entry this step "
        "anchors on. Re-point the injection rather than letting it silently no-op."
    )
out = out.replace(
    anchor,
    "        - name: KMS_CRYPTO_KEY\n          value: KMS_CRYPTO_KEY_VALUE\n"
    "        - name: KMS_INSECURE_TEST_ENDPOINT\n          value: $STUB:8200")

out = out.replace("CLUSTER_NAME_VALUE", "$CLUSTER")
out = out.replace("KMS_CRYPTO_KEY_VALUE", "$CRYPTO_KEY")
if "KMS_INSECURE_TEST_ENDPOINT" not in out:
    raise SystemExit("the test endpoint was not injected; the plugin would dial real Cloud KMS")
# The locally built tag, and imagePullPolicy Never so kind cannot reach for the
# registry copy and silently test a different binary.
out = re.sub(r"image: ghcr\.io/soloz-io/kms-plugin@sha256:[0-9a-f]{64}", "image: $IMAGE", out)
out = out.replace("imagePullPolicy: IfNotPresent", "imagePullPolicy: Never")
if "_VALUE" in out or "ghcr.io" in out:
    raise SystemExit("a substitution marker survived; the run would not be testing what it claims")
pathlib.Path("static-pod.rendered.yaml").write_text(out)
PY
ok "rendered from deploy/static-pod.yaml"

# A PLACEHOLDER credential configuration. The stub needs no authentication -- that is
# what KMS_INSECURE_TEST_ENDPOINT turns off -- so this exists only so the mount the
# production pod declares is present, and the pod spec is not special-cased for the
# gate. Proving a real federated identity is Gate 3's job and no file here can do it.
printf '{"_comment":"placeholder; the gate stub requires no authentication"}\n' > credentials.json
chmod 600 credentials.json
ok "placeholder credential configuration written 0600"

# ABSOLUTE PATHS FOR THE BIND MOUNTS. See the comment in kind.yaml: a relative
# hostPath that does not resolve makes Docker create a DIRECTORY at the destination,
# which is silent and breaks all three mounts at once.
sed "s#GATE1_DIR#$PWD#g" kind.yaml > kind.rendered.yaml
grep -q GATE1_DIR kind.rendered.yaml && { bad "a path marker survived"; exit 1; }
ok "kind config rendered with absolute mount paths"

say "4. create the cluster with the KMS provider configured"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true

# A GENUINE ORDERING PROBLEM, AND WHY CREATION RUNS IN THE BACKGROUND.
#
# `kubeadm init` blocks on the API server becoming healthy. The API server cannot
# serve until the plugin answers on the socket. The plugin is a static pod with
# imagePullPolicy: Never, so it needs its image already in the node's containerd --
# and `kind load` cannot run until the node container exists, which happens partway
# through creation.
#
# So creation is started in the background and the image is loaded as soon as the node
# container appears. kubelet retries the static pod and kubeadm retries the API server,
# which is exactly what a real node does while an image is still pulling. Loading it
# beforehand is impossible; creating the cluster without the provider and adding it
# afterwards would be a different and weaker test.
# --retain, because WITHOUT IT KIND DELETES THE NODE ON FAILURE. The trap in this
# script keeps the environment on a failed step, and kind was removing the node
# container before that could matter -- so the first real failure here left nothing to
# look at: no node, no plugin logs, no files to stat.
kind create cluster --config kind.rendered.yaml --retain --wait 120s >/dev/null 2>&1 &
CREATE_PID=$!

for _ in $(seq 1 60); do
  docker inspect "${CLUSTER}-control-plane" >/dev/null 2>&1 && break
  sleep 2
done
if ! docker inspect "${CLUSTER}-control-plane" >/dev/null 2>&1; then
  bad "the kind node container never appeared"
  exit 1
fi
# Retried: containerd inside the node takes a moment after the container starts.
for _ in $(seq 1 30); do
  kind load docker-image "$IMAGE" --name "$CLUSTER" >/dev/null 2>&1 && break
  sleep 2
done
ok "plugin image loaded into the node while kubeadm was still initialising"
wait "$CREATE_PID" 2>/dev/null || true

# THE MOUNTS ARE VERIFIED BEFORE ANYTHING ELSE, so a bad bind is named as one.
# Without this the failure presents as "API server never became ready", which sends
# the reader to the plugin rather than to a directory where a file should be.
for f in /etc/kubernetes/enc/enc.yaml /etc/kubernetes/manifests/kms-plugin.yaml \
         /etc/kubernetes/kms/credentials.json; do
  if docker exec "${CLUSTER}-control-plane" test -f "$f" 2>/dev/null; then
    ok "mounted as a file: $f"
  else
    bad "$f is NOT a regular file on the node — a bind source that does not resolve makes Docker create a directory there"
    docker exec "${CLUSTER}-control-plane" ls -la "$(dirname "$f")" 2>/dev/null || true
    exit 1
  fi
done

# Now wait for the API server, which cannot serve until the plugin answers.
for _ in $(seq 1 90); do
  kubectl --context "kind-$CLUSTER" get --raw /readyz >/dev/null 2>&1 && break
  sleep 2
done
if kubectl --context "kind-$CLUSTER" get --raw /readyz >/dev/null 2>&1; then
  ok "API server is serving with the KMS provider in force"
else
  bad "API server never became ready — the plugin is on its start path"
  docker exec "${CLUSTER}-control-plane" crictl ps -a 2>/dev/null | head -10 || true
  docker logs "$STUB" 2>&1 | tail -20
  exit 1
fi

say "5. the plugin is the non-root static pod it claims to be"
POD=$(kubectl --context "kind-$CLUSTER" -n kube-system get pod -l component=kms-plugin \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[ -n "$POD" ] && ok "static pod adopted: $POD" || bad "no kms-plugin pod is visible"
UID_SEEN=$(docker exec "${CLUSTER}-control-plane" sh -c \
  'ps -o user= -C kms-plugin 2>/dev/null | head -1' 2>/dev/null || true)
printf '  (process owner inside the node: %s)\n' "${UID_SEEN:-unknown}"
docker exec "${CLUSTER}-control-plane" stat -c '%a %u:%g' /var/run/kms 2>/dev/null \
  | grep -q '^700 65532:65532' \
  && ok "socket dir is 0700 owned by 65532 — the initContainer did its job" \
  || bad "socket dir is $(docker exec "${CLUSTER}-control-plane" stat -c '%a %u:%g' /var/run/kms 2>/dev/null)"

say "6. a Secret is stored under the KMS provider"
kubectl --context "kind-$CLUSTER" -n default create secret generic enc-probe \
  --from-literal=k=v >/dev/null
# ETCDCTL IS IN THE ETCD POD, NOT ON THE NODE. An earlier version of this ran
# `docker exec` against the node's filesystem, where there is no etcdctl, so RAW came
# back EMPTY and the step reported "etcd does not hold the KMS prefix" about a cluster
# that did. Querying through the pod is also exactly the form the production runbook
# uses (docs/runbooks/encrypt-secrets-at-rest.md step 8), so the two agree.
# NO `sh -c`, BECAUSE THE etcd IMAGE HAS NO SHELL. The second attempt at this step
# wrapped the command in `sh -c` and got:
#
#   exec: "sh": executable file not found in $PATH
#
# which returned empty and reported "etcd does not hold the KMS prefix" about a cluster
# that did -- the same false negative as the `docker exec` version before it, for a
# different reason. etcdctl is invoked directly; ETCDCTL_API=3 is unnecessary because
# etcdctl v3.5+ defaults to the v3 API, and setting it would have needed the shell this
# image does not have. The pipeline after the exec runs on the HOST, which does have
# head and tr.
RAW=$(kubectl --context "kind-$CLUSTER" -n kube-system exec "etcd-${CLUSTER}-control-plane" -- \
  etcdctl --cacert /etc/kubernetes/pki/etcd/ca.crt \
     --cert /etc/kubernetes/pki/etcd/server.crt \
     --key /etc/kubernetes/pki/etcd/server.key \
     get /registry/secrets/default/enc-probe 2>/dev/null | head -c 400 | LC_ALL=C tr -d '\0' || true)
case "$RAW" in
  *k8s:enc:kms:v2:soloz-kms:*) ok "etcd holds k8s:enc:kms:v2:soloz-kms:" ;;
  *) bad "etcd does not hold the KMS prefix; got: $(printf '%s' "$RAW" | head -c 80)" ;;
esac

say "7. THE RESTART, which is the row that catches the DEK-cache defect"
# A plugin returning the wrong bytes from Unwrap passes a write-then-read test: the
# write is correct and the read is served from the API server's DEK cache. It fails
# only once that cache is cold.
docker exec "${CLUSTER}-control-plane" sh -c \
  'mv /etc/kubernetes/manifests/kube-apiserver.yaml /tmp/ka.yaml' 2>/dev/null || true
sleep 12
docker exec "${CLUSTER}-control-plane" sh -c \
  'mv /tmp/ka.yaml /etc/kubernetes/manifests/kube-apiserver.yaml' 2>/dev/null || true
for _ in $(seq 1 90); do
  kubectl --context "kind-$CLUSTER" get --raw /readyz >/dev/null 2>&1 && break
  sleep 2
done
VAL=$(kubectl --context "kind-$CLUSTER" -n default get secret enc-probe \
  -o jsonpath='{.data.k}' 2>/dev/null | base64 -d 2>/dev/null || true)
[ "$VAL" = "v" ] \
  && ok "a Secret written BEFORE the restart decrypts after it — the DEK round trip is byte-correct" \
  || bad "the Secret did not decrypt after the API server restarted (got ${VAL:-<nothing>})"

say "8. rotation: the identifier changes and old ciphertext still decrypts"
# ROTATION GOES TO THE ADMIN PORT, NOT THE gRPC PORT, and the separation is the point:
# ADR-100 forbids the plugin any permission to rotate, so a rotate call on the surface
# the plugin talks to would let a mistaken plugin trigger one and the gate would not
# notice.
BEFORE=$(docker logs "$STUB" 2>&1 | grep -c 'ROTATED' || true)
docker exec "$STUB" sh -c "wget -qO- http://localhost:8201/rotate" >/dev/null 2>&1 || true
AFTER=$(docker logs "$STUB" 2>&1 | grep -c 'ROTATED' || true)
[ "$AFTER" -gt "$BEFORE" ] && ok "the key rotated in the store" || bad "rotation did not happen"

# WHAT THE PLUGIN REPORTS, BEFORE THE ROTATION PROPAGATES. The step's title claims the
# identifier changes, and for a long time nothing here checked that -- it checked that
# the stub rotated and that reads and writes still worked, both of which would also hold
# if the plugin had never noticed. A rotation whose identifier does not change is
# precisely the vendor-plugin defect ADR-100 exists to fix, and it is invisible from the
# data: old Secrets decrypt, new writes succeed, and the API server keeps the data keys
# established under the old version forever.
plugin_log() {
  local id
  id=$(docker exec "${CLUSTER}-control-plane" crictl ps -a --name kms-plugin -q 2>/dev/null | head -1)
  [ -n "$id" ] && docker exec "${CLUSTER}-control-plane" crictl logs "$id" 2>&1 || true
}
KEYID_BEFORE=$(plugin_log | grep -o 'key_id reported to the API server: [^)]*' | tail -1)

# 35 SECONDS, AND THE NUMBER COMES FROM THE API SERVER, NOT FROM THE PLUGIN.
#
# The plugin's own --refresh-interval is 10 minutes and is only a safety net. What
# actually re-reads the key version here is the API server's Status poll, whose TTL is
# 20s while the provider is healthy (k8s.io/apiserver encryptionconfig/config.go:96).
# 35s is that TTL plus margin. Do not "fix" this by matching it to --refresh-interval.
sleep 35
kubectl --context "kind-$CLUSTER" -n default create secret generic enc-probe-2 \
  --from-literal=k=w >/dev/null 2>&1 || true

KEYID_AFTER=$(plugin_log | grep -o 'key_id reported to the API server: [^)]*' | tail -1)
if [ -z "$KEYID_BEFORE" ] || [ -z "$KEYID_AFTER" ]; then
  bad "could not read the reported key_id from the plugin's log, so the rotation cannot be verified"
elif [ "$KEYID_BEFORE" = "$KEYID_AFTER" ]; then
  bad "the reported key_id did not change across a rotation ($KEYID_AFTER) -- the API server cannot tell anything happened"
else
  ok "the reported key_id changed across the rotation"
  printf '       before: %s\n       after:  %s\n' "${KEYID_BEFORE#key_id reported to the API server: }" "${KEYID_AFTER#key_id reported to the API server: }"
fi
OLD=$(kubectl --context "kind-$CLUSTER" -n default get secret enc-probe \
  -o jsonpath='{.data.k}' 2>/dev/null | base64 -d 2>/dev/null || true)
NEW=$(kubectl --context "kind-$CLUSTER" -n default get secret enc-probe-2 \
  -o jsonpath='{.data.k}' 2>/dev/null | base64 -d 2>/dev/null || true)
[ "$OLD" = "v" ] && ok "pre-rotation ciphertext still decrypts" || bad "pre-rotation Secret is unreadable after rotation"
[ "$NEW" = "w" ] && ok "a post-rotation write round-trips" || bad "a post-rotation write failed"

say "9. outage: what KMS v2 actually does when the key store is unreachable"
# TWO CORRECTIONS FROM THE FIRST REAL RUN.
#
# (a) THE EXPECTATION WAS BACKWARDS. This step used to assert that a new Secret write
#     FAILS during an outage. It does not, and that is by design: KMS v2 has the API
#     server wrap a DEK once and reuse it for many objects, precisely so the key store
#     is not in the path of every write. A write succeeding during a brief outage is
#     the resilience the design is for, not a failure to fail closed.
#
#     So the write is RECORDED rather than judged, and the real fail-closed property
#     is tested where it actually lives: a cold read. The API server is restarted while
#     the store is unreachable, which empties the DEK cache and forces an unwrap.
#
# (b) `docker stop` LOST THE STUB'S STATE. Its key version is in memory, so a restart
#     came back at v1 after having rotated to v2, and the recovery check was then
#     measuring an unrelated condition. A real key store does not regress its version;
#     disconnecting the network produces a genuine connection failure while the process
#     keeps its state.
#
#     THIS COMMENT USED TO SAY the plugin "correctly refused to reactivate a previous
#     version", and that behaviour has been removed deliberately. Enforcing it here was
#     enforcement that lapsed on every restart and every control-plane node replacement,
#     because the record of which versions had been active lived in process memory. The
#     invariant now belongs to the rotation authority -- the only identity permitted to
#     call UpdateCryptoKeyPrimaryVersion -- and the plugin reports what Cloud KMS says
#     and logs a warning when it holds evidence of a regression. So a stub restart would
#     now be absorbed SILENTLY, which is a better reason to avoid it than the old one.
docker network disconnect kind "$STUB" >/dev/null 2>&1 || true
sleep 5

if kubectl --context "kind-$CLUSTER" -n default create secret generic enc-probe-3 \
     --from-literal=k=x >/dev/null 2>&1; then
  printf '  \033[33mNOTE\033[0m a write SUCCEEDED during the outage — the API server reused a cached DEK.\n'
  printf '       This is KMS v2 behaving as designed, and it bounds the exposure: writes\n'
  printf '       survive an outage shorter than the DEK cache lifetime.\n'
else
  printf '  \033[33mNOTE\033[0m a write FAILED during the outage — no usable cached DEK at that moment.\n'
fi

say "9b. the real fail-closed test: a COLD read while the store is unreachable"
# Restarting the API server empties its DEK cache, so reading any Secret now requires
# the plugin, which requires the key store. This must NOT quietly succeed.
docker exec "${CLUSTER}-control-plane" sh -c \
  'mv /etc/kubernetes/manifests/kube-apiserver.yaml /tmp/ka.yaml' 2>/dev/null || true
sleep 12
docker exec "${CLUSTER}-control-plane" sh -c \
  'mv /tmp/ka.yaml /etc/kubernetes/manifests/kube-apiserver.yaml' 2>/dev/null || true
sleep 45
if kubectl --context "kind-$CLUSTER" -n default get secret enc-probe -o jsonpath='{.data.k}' >/dev/null 2>&1; then
  bad "a Secret was READ from a cold cache while the key store was unreachable — that is not fail-closed"
else
  ok "a cold read fails while the key store is unreachable — the cluster fails CLOSED"
fi

say "9c. recovery"
docker network connect kind "$STUB" >/dev/null 2>&1 || true
for _ in $(seq 1 90); do
  kubectl --context "kind-$CLUSTER" get --raw /readyz >/dev/null 2>&1 && break
  sleep 2
done
VAL2=$(kubectl --context "kind-$CLUSTER" -n default get secret enc-probe \
  -o jsonpath='{.data.k}' 2>/dev/null | base64 -d 2>/dev/null || true)
[ "$VAL2" = "v" ] \
  && ok "reads recover once the key store returns, with no operator action" \
  || bad "the cluster did not recover after the key store returned (got ${VAL2:-<nothing>})"

say "result"
printf '  %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1

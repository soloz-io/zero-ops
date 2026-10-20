#!/bin/sh
# ADR-107 sandboxed-runtime installer. Runs IN THE INSTALLER CONTAINER (busybox)
# with the node root at /host; see runtime.yaml. Delivered as a generated
# ConfigMap (kustomization.yaml), so any change here re-runs it on every node.
set -eu
RELEASE=20261012
# The NODE's architecture, from its kernel (this container shares it). The
# release for another architecture would still execute under binary
# translation (Rosetta on Apple Silicon registers itself for x86_64) and then
# fail inside gVisor, which supports only native execution -- so the binary
# must match the kernel, and an unlisted architecture is refused.
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  SHA512=9606445eed5aedfcf05310e7ccef61d994089914c6622fc82624c24d2b18db1b2210f40e40a7be27b8b7772859de7d95c501b4eba66423053c960bf671377c5e ;;
  aarch64) SHA512=2fe7425abd16ad6ca649ab9bc3d7e8e76756834d86ffcc08dbe97c82c3b7262a7d6401f23632f2b296649cc1408b560416b2ab65a4043aa123fc7b90f3a1b2e2 ;;
  *) echo "node architecture $ARCH has no pinned gVisor $RELEASE release" >&2; exit 1 ;;
esac
URL="https://storage.googleapis.com/gvisor/releases/release/${RELEASE}/${ARCH}/gvisor.tar.bz2"
CONFIG="${CONTAINERD_CONFIG:?the containerd config path is required}"
H=/host
ROOT=/opt/gvisor                # one directory per release, as released
DEST="$ROOT/$RELEASE-$ARCH"
BEGIN="# BEGIN zero-ops ADR-107 sandboxed runtime"
END="# END zero-ops ADR-107 sandboxed runtime"
LEGACY="# zero-ops ADR-107 sandboxed runtime"   # first format: marker to EOF
host() { chroot "$H" /usr/bin/env PATH="/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" "$@"; }

[ -f "$H$CONFIG" ] || { echo "containerd config $CONFIG does not exist on the node" >&2; exit 1; }

# The release is installed AS RELEASED, as a tree. runsc loads helper
# binaries relative to its own location (gvisor-bin/ beside it); copying
# runsc out alone produces a runtime that registers and then fails every
# sandbox ("sidecar gvisor_sentry not usable"). Nothing here assumes the
# tree's layout beyond the two binaries it locates by name.
if [ ! -f "$H$DEST/.complete" ]; then
  tmp="/tmp/adr107-$$"
  trap 'rm -rf "$H$tmp" /work' EXIT
  mkdir -p "$H$tmp" /work
  # The node's curl: busybox's wget has no TLS this can rely on.
  host curl -fsSL --retry 3 -o "$tmp/r.tar.bz2" "$URL"
  echo "${SHA512}  $H$tmp/r.tar.bz2" | sha512sum -c -
  tar -xjf "$H$tmp/r.tar.bz2" -C /work
  rm -rf "$H$DEST.new" && mkdir -p "$H$DEST.new"
  cp -a /work/. "$H$DEST.new/"
  rm -rf "$H$DEST" && mv "$H$DEST.new" "$H$DEST"
  touch "$H$DEST/.complete"
fi
locate() {
  n=$(find "$H$DEST" -type f -name "$1" | wc -l)
  [ "$n" -eq 1 ] || { echo "release $RELEASE has $n files named $1; expected exactly one" >&2; exit 1; }
  f=$(find "$H$DEST" -type f -name "$1"); chmod 0755 "$f"; echo "${f#$H}"
}
# Trees of any other release or architecture are stale by definition: the
# configuration below names only this one.
for d in "$H$ROOT"/*; do
  [ -e "$d" ] && [ "$d" != "$H$DEST" ] && rm -rf "$d"
done
RUNSC=$(locate runsc)
SHIM=$(locate containerd-shim-runsc-v1)
"$H$RUNSC" --version | head -1

cat > "$H/etc/containerd/runsc.toml" <<EOF
binary_name = "$RUNSC"
[runsc_config]
  platform = "systrap"
EOF

# The CRI plugin's id differs between config versions: version 3 (the
# containerd 2 native format) and version 2 (containerd 1.7, and the Kind
# node image).
if grep -Eq '^version *= *3' "$H$CONFIG"; then
  P="plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc"
elif grep -Eq '^version *= *2' "$H$CONFIG"; then
  P='plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc'
else
  echo "$CONFIG states no config version 2 or 3; refusing to guess" >&2; exit 1
fi
BLOCK=$(cat <<EOF
$BEGIN
[$P]
  runtime_type = "io.containerd.runsc.v1"
  runtime_path = "$SHIM"
[$P.options]
  TypeUrl = "io.containerd.runsc.v1.options"
  ConfigPath = "/etc/containerd/runsc.toml"
$END
EOF
)
# The block this installer owns, as it stands: between the markers, or
# (first format) from its marker to end of file, which is where that
# format always appended it.
current=$(awk -v b="$BEGIN" -v e="$END" -v l="$LEGACY" '
  $0==b {on=1} on {print} $0==e {on=0}
  $0==l {legacy=1} legacy {print}' "$H$CONFIG")
if [ "$current" != "$BLOCK" ]; then
  [ -f "$H$CONFIG.pre-adr107" ] || cp -p "$H$CONFIG" "$H$CONFIG.pre-adr107"
  awk -v b="$BEGIN" -v e="$END" -v l="$LEGACY" '
    $0==b {skip=1} $0==l {legacy=1}
    !skip && !legacy {print}
    $0==e {skip=0}' "$H$CONFIG" > "$H$CONFIG.adr107"
  printf '\n%s\n' "$BLOCK" >> "$H$CONFIG.adr107"
  mv "$H$CONFIG.adr107" "$H$CONFIG"
  # Running containers survive this: each is held by its own shim, not by
  # the daemon. The kubelet sees the CRI drop for the restart and
  # reconnects.
  host systemctl restart containerd
fi

# Binaries of the first format, which nothing references any more.
rm -f "$H/opt/bin/runsc" "$H/opt/bin/containerd-shim-runsc-v1"

i=0
until host crictl info 2>/dev/null | grep -q '"runsc"'; do
  i=$((i+1)); [ $i -lt 30 ] || { echo "containerd does not report the runsc handler" >&2; exit 1; }
  sleep 2
done
echo "sandboxed runtime ready: runsc $RELEASE $ARCH registered ($RUNSC)"

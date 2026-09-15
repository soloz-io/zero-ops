#!/usr/bin/env bash
# 03-platform-services-appset.yaml patches /containers/0/args/0 and /args/1 by
# INDEX. Reordering the args does not fail the patch — it rewrites the wrong
# flag, and external-dns then runs with a valid-looking but wrong domain filter.
# The arg order is therefore a contract, and this is where it is enforced.
# The HUB's external-dns, which templated-fields.yaml rewrites BY INDEX.
#
# Three separate contracts, and only the first was checked:
#
#   args[0] --domain-filter    rewritten by replaceAll
#   args[1] --txt-owner-id     rewritten by replaceAll
#   args[4] --provider         rewritten BY PATH -- [.., args, 4]
#
# A path-targeted rewrite is silently wrong if the list is reordered: it would
# overwrite whichever flag now sits at 4. And the value matters as much as the
# position -- external-dns's --provider is an ENUM (akamai, ..., webhook) with no
# "hetzner" member, so the platform selector `dns.provider: hetzner` must be
# translated to `webhook` rather than passed through.
#
# Passing it through unmapped cost 108 crash-loops of
# "flag parsing error: enum value must be one of ...", during which no hostname
# for the box was published and every hub URL was unreachable. Nothing reported
# it: the Application stayed Synced because the Deployment existed.
validate_hub_external_dns_args() {
    section "hub external-dns provider contract"
    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY_INNER'
import yaml

manifest = "manifests/hub-core-services/external-dns/external-dns.yaml"
rule = "manifests/hub-core-services/external-dns/templated-fields.yaml"

args = None
for doc in yaml.safe_load_all(open(manifest)):
    if doc and doc.get("kind") == "Deployment":
        args = doc["spec"]["template"]["spec"]["containers"][0].get("args", [])
        break

if args is None:
    print("BAD no external-dns Deployment in %s" % manifest)
elif len(args) < 5:
    print("BAD fewer than five args; the provider rewrite targets args[4]")
elif not args[0].startswith("--domain-filter="):
    print("BAD args[0] is %r, expected --domain-filter=" % args[0])
elif not args[1].startswith("--txt-owner-id="):
    print("BAD args[1] is %r, expected --txt-owner-id=" % args[1])
elif not args[4].startswith("--provider="):
    print("BAD args[4] is %r, expected --provider= -- the templated rewrite "
          "targets this index and would overwrite the wrong flag" % args[4])
elif args[4] != "--provider=webhook":
    print("BAD args[4] is %r; the shipped literal must be --provider=webhook, "
          "which is what reaches Hetzner DNS through the sidecar" % args[4])
else:
    text = open(rule).read()
    if "args, 4]" not in text:
        print("BAD the templating no longer targets args[4]")
    elif 'eq .Values.global.dns.provider "hetzner"' not in text:
        print("BAD the provider selector is not translated: external-dns has no "
              "'hetzner' provider, so dns.provider must map to webhook")
    else:
        print("OK")
PY_INNER
)
    case "$out" in
        OK) pass "hub external-dns: args[4] is --provider and the selector is translated" ;;
        *)  hard_fail "hub external-dns provider contract: ${out#BAD }" ;;
    esac
}

validate_external_dns_args() {
    section "external-dns args[0]/args[1] index contract"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import yaml
f = "manifests/spoke/spoke-catalog/infra/external-dns.yaml"
for doc in yaml.safe_load_all(open(f)):
    if not doc or doc.get("kind") != "Deployment":
        continue
    args = doc["spec"]["template"]["spec"]["containers"][0].get("args", [])
    if len(args) < 2:
        print("BAD fewer than two args"); break
    if not args[0].startswith("--domain-filter="):
        print("BAD args[0] is %r, expected --domain-filter=" % args[0]); break
    if not args[1].startswith("--txt-owner-id="):
        print("BAD args[1] is %r, expected --txt-owner-id=" % args[1]); break
    print("OK")
    break
PY
)
    if [[ "$out" == OK* ]]; then
        pass "external-dns args[0]=--domain-filter args[1]=--txt-owner-id (AppSet patches by index)"
    else
        hard_fail "external-dns arg order violates the AppSet index patch: ${out#BAD }"
    fi
}

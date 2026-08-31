No baseDomain value exists, and hub hostnames are already hardcoded elsewhere (01-platform-infra-appset.yaml:54 hardcodes argocd.dev.nutgraf.in). Templating them is a platform-wide ADR-051 change, not part of this retirement — so let me record the constraint accurately rather than half-solve it:


No — don't restore it. The two clusters genuinely install cert-manager differently, and each file is now correct for its own cluster:

HUB    SAs: cert-manager, cert-manager-cainjector, cert-manager-webhook
       Deployment serviceAccountName: cert-manager
SPOKE  SAs: cm-cert-manager, cm-cert-manager-cainjector, cm-cert-manager-webhook, cm-cert-manager-startupapicheck

The spoke's Helm release is named cm, so every object gets that prefix. The hub's isn't, so nothing does. That's not a mistake another session made — it's how the two releases were installed, and it's visible in the live SA lists.

The evidence is direct: with cm-cert-manager in the hub's binding, challenges failed with

hetzner.acme.nutgraf.in is forbidden:
User "system:serviceaccount:cert-manager:cert-manager" cannot create resource "hetzner"

The hub's cert-manager literally identifies as cert-manager. After changing the subject, all four certificates issued. Restoring cm- would re-break issuance on the hub while fixing nothing.

So the current split is right:
- hub-core-services/ingress/… → cert-manager
- spoke/spoke-catalog/provider/hetzner/… → cm-cert-manager

The real risk is that they look like they should match. The files are otherwise byte-identical, so anyone diffing them will see one line out of step and "fix" it — and the breakage surfaces only at challenge time, with the Certificate showing nothing but Ready=False and no hint about RBAC. I put a comment on that line in the hub copy explaining exactly this. If you'd rather make it structural than advisory, the durable options are to add the other SA name as a second subject in both copies (harmless if it doesn't exist), or to rename the hub's release to cm so the two converge — though renaming a live cert-manager release is disruptive for no functional gain.

My recommendation: leave it as-is. It's correct, it's explained at the point of divergence, and it's verified working.
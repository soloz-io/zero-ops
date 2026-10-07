# The hub's own encryption key, reconciled

ADR-100 "Declarative delivery". These objects hold the key that encrypts the hub's etcd
to its desired state. They do **not** create it.

## Why management policies, and why these ones

| resource | policy | reason |
|---|---|---|
| the hub's `KeyRing` and `CryptoKey` | **`Observe` only** | see below — this was `Observe, Update` and was narrowed deliberately |
| anything here | never `Create` | Crossplane cannot create the key the cluster it runs in depends on. The hub's API server cannot decrypt until the plugin reaches this key, so creation is bootstrap (`soloz kms init`) and this adopts it |
| anything here | never `Delete`, `deletionPolicy: Orphan` | a `CryptoKey` removed from Git must not schedule key destruction. Destroying a version makes every backup taken under it unreadable, which ADR-100 calls the same loss as losing the key |

### Why not `Update`

To update, the reconciler needs `cloudkms.cryptoKeys.update` **on the key that encrypts
this cluster's own etcd**, held by a workload running inside that cluster. It could not
decrypt anything — no crypto permissions — but it could change the rotation period and the
destroy-scheduled window, which are two of the three things between a mistake and
unreadable backups. Compromising the hub should not also hand over the controls on the key
protecting the hub.

It is the same reasoning that makes the key's IAM policy observe-only, applied one level
in.

**What it costs:** Git holds the desired state and the reconciler holds the alarm, not the
pen. Remediation is a deliberate `soloz kms` action using an operator credential from
outside the cluster. And the alarm itself — comparing `status.atProvider` against the spec
— is **not built**; it is the drift control still open under ADR-100 condition 6.

Spoke keys are different and are **not** here: the hub creates a spoke's key before the
spoke exists, so there is no circularity and the full lifecycle is available. They belong
with the spoke composition.

## What this buys that the CLI does not

`soloz kms init` sets the rotation period and the destroy window once. Nothing afterwards
notices if they change. These objects are the reconciler the review asked for: the
rotation period, the destroy window and the key's existence become drift that is detected
and reported rather than a state somebody set in March.

## Not wired yet, and what has to be settled first

`providers/kustomization.yaml` does not list `provider-gcp/provider.yaml`, so none of this
installs. Three things come first:

1. **The ProviderConfig's credential.** It must NOT be the platform's established pattern
   of ESO pulling a secret from Infisical: that puts Infisical back in the KEK's trust
   path one hop removed, and ADR-100 rejected it as the key authority for less. Crossplane
   is an ordinary Deployment so it *can* consume a projected ServiceAccount token, which
   is precisely what the KMS plugin cannot do — but the GCP ProviderConfig's field shape
   for that is **unverified**.
2. **Where the reconciler runs.** Crossplane is on the hub, which is correct for spoke
   keys. For the hub's own key it is safe only because the policy above forbids creation —
   steady-state reconciliation of an existing key is not circular, creation would be.
3. **The hub-rebuild trust-anchor step**, which is a procedure with no enforcement. A
   rebuild that silently omits it produces a hub that starts and cannot read a Secret.

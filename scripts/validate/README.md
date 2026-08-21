# Validation modules

Two phases, one module per concern.

```
run.sh preflight                    static; no cluster needed; fatal
run.sh cluster [--mode=gate|final]  live cluster
run.sh cluster --only=name,name     subset, used for in-flight gates
```

A module's name is its filename minus the numeric prefix and `.sh`
(`40-tenant-ingress.sh` → `tenant-ingress`). Numeric prefixes only fix ordering.

## Where each phase runs

| Phase | Called from | On failure |
|---|---|---|
| `preflight` | `hub-bootstrap.sh`, before anything is created | aborts; nothing provisioned |
| `cluster --mode=gate` | `hub-bootstrap.sh`, at the step that makes each condition decidable | aborts at that step |
| `cluster --mode=final` | `post-bootstrap-validate.sh` | non-zero exit |

## Severity

`VALIDATE_MODE` exists because the same condition means different things at
different moments. Mid-bootstrap a resource that has not appeared yet is not a
defect; afterwards it is.

- `hard_fail` — wrong regardless of timing (a wrong environment slug will not
  converge into the right one). Fails in both modes.
- `soft_fail` — warns in `gate`, fails in `final`.
- `warn` — never fails.

One implementation therefore serves both moments, instead of two copies drifting.

## Adding a check

Add a file to `preflight/` or `cluster/` defining `validate_<name>()`. `run.sh`
discovers it; nothing else needs editing.

State in a comment *why* the check exists. Every module here encodes a failure
that reported Healthy while being broken — that is the bar. Anything an existing
`kubectl` status, kustomize build, or Go test already catches does not belong here.

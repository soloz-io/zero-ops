#!/usr/bin/env bash
# The base HubEnvironment carries environmentSlug: dev. An overlay with no patch
# inherits it — which is how prod would have read dev's secrets out of Infisical
# while every condition on the cluster reported Healthy. Nothing downstream can
# tell correct resolution from resolution against the wrong environment.
validate_environment_slug() {
    section "Per-environment Infisical slug"

    local env patch
    for env in dev stg prod; do
        patch="$VALIDATE_ROOT/manifests/environments/$env/patch-hubenvironment.yaml"
        if [[ ! -f "$patch" ]]; then
            hard_fail "$env has no patch-hubenvironment.yaml — it inherits environmentSlug 'dev' from base"
            continue
        fi
        if grep -qE "^[[:space:]]*environmentSlug:[[:space:]]*$env[[:space:]]*$" "$patch"; then
            pass "$env pins environmentSlug: $env"
        else
            hard_fail "$env/patch-hubenvironment.yaml does not set environmentSlug: $env"
        fi
        if ! grep -q "patch-hubenvironment.yaml" "$VALIDATE_ROOT/manifests/environments/$env/kustomization.yaml" 2>/dev/null; then
            hard_fail "$env/kustomization.yaml does not reference patch-hubenvironment.yaml — the patch is inert"
        fi
    done
}

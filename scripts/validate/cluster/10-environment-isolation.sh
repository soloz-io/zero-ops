#!/usr/bin/env bash
# A wrong environmentSlug makes every ExternalSecret on this hub resolve against
# another environment's Infisical — while reporting Healthy. There is no
# behavioural signal that distinguishes it from correct operation, which is why
# it is asserted explicitly and why a mismatch is a hard failure in both modes:
# a wrong slug does not converge into the right one.
validate_environment_isolation() {
    section "Environment isolation (Infisical slug)"

    local he
    he=$(kc get hubenvironment -A -o jsonpath='{.items[0].spec.secrets.infisical.environmentSlug}')
    if [[ -z "$he" ]]; then
        soft_fail "HubEnvironment not present, so the Infisical slug cannot be verified"
    elif [[ "$he" == "$ENVIRONMENT" ]]; then
        pass "HubEnvironment environmentSlug=$he"
    else
        hard_fail "HubEnvironment environmentSlug=$he but this is the $ENVIRONMENT hub — it reads another environment's secrets"
    fi

    # The store resource is named infisical-backend. "platform-cluster-secret-store"
    # is the ArgoCD Application that delivers it — querying that name finds nothing
    # and the check silently passes as "not created yet".
    local css
    css=$(kc get clustersecretstore infisical-backend \
        -o jsonpath='{.spec.provider.infisical.secretsScope.environmentSlug}')
    if [[ -z "$css" ]]; then
        soft_fail "ClusterSecretStore infisical-backend not created, so its Infisical slug cannot be verified"
    elif [[ "$css" == "$ENVIRONMENT" ]]; then
        pass "ClusterSecretStore infisical-backend environmentSlug=$css"
    else
        hard_fail "ClusterSecretStore infisical-backend environmentSlug=$css, expected $ENVIRONMENT"
    fi
}

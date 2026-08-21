#!/usr/bin/env bash
# platform-db holds Infisical: the PKI root and every platform secret. It has
# previously run with lastSuccessfulBackup empty and nothing reported it.
#
# The severity depends on the storage class, because the same empty field means
# very different things: on hcloud-volumes the data survives a reschedule and the
# missing backup is a gap; on local-path it is the only copy, and losing the node
# loses the platform.
validate_root_of_trust_backup() {
    section "Backup of the root of trust (platform-db)"

    local sc last
    sc=$(kc get cluster.postgresql.cnpg.io platform-db -n platform-data \
        -o jsonpath='{.spec.storage.storageClass}')
    last=$(kc get cluster.postgresql.cnpg.io platform-db -n platform-data \
        -o jsonpath='{.status.lastSuccessfulBackup}')

    if [[ -n "$last" ]]; then
        pass "platform-db lastSuccessfulBackup=$last"
        return 0
    fi

    if [[ "$sc" == "local-path" ]]; then
        soft_fail "platform-db has NEVER backed up successfully AND is on local-path — the PKI root and every platform secret exist only on one node's disk"
    else
        warn "platform-db has never backed up successfully (storageClass=${sc:-unknown})"
        note "seed S3_ACCESS_KEY_ID / S3_SECRET_ACCESS_KEY in Infisical to enable Barman backups"
    fi
}

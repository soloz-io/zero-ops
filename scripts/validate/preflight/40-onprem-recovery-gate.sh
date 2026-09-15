#!/usr/bin/env bash
# provision-flatcar-worker.sh is destructive: phase_prep_flatcar_and_ignition
# deletes the Node object and sweeps the VM, phase_provision_flatcar_vm replaces
# the disk from the pristine base image. Together they cost ~10 minutes and take
# with them the evidence for why the node dropped out.
#
# For most of this script's life that was the ONLY thing it could do — there was
# no branch for "the VM exists and is merely Off". On 2026-09-15 a hub worker
# went NotReady because its Hyper-V host could not hold its memory reservation,
# and the repair for that was to destroy the VM and rebuild it from scratch; the
# rebuild then failed on the same reservation race, leaving the box with no
# worker at all.
#
# phase_recover_existing_vm is the missing branch. It is only load-bearing if it
# runs BEFORE everything that destroys, which is an ordering no runtime check can
# observe (by the time it is wrong, the disk is gone). So it is asserted here.
validate_onprem_recovery_gate_runs_first() {
    section "on-prem recovery gate precedes every destructive phase"
    local script="$VALIDATE_ROOT/scripts/hybrid/provision-flatcar-worker.sh"

    if [[ ! -f "$script" ]]; then
        hard_fail "provision-flatcar-worker.sh not found at $script"
        return
    fi

    # Line numbers of the per-node loop's phase calls. Comments are stripped so
    # this cannot match its own rationale, or the script's.
    local body gate prep ign vm
    body=$(grep -vE '^\s*#' "$script")

    gate=$(printf '%s\n' "$body" | grep -n 'phase_recover_existing_vm "\$SSH_TARGET"' | head -1 | cut -d: -f1)
    prep=$(printf '%s\n' "$body" | grep -n 'phase_prep_hyperv "\$SSH_TARGET"' | head -1 | cut -d: -f1)
    ign=$(printf '%s\n' "$body" | grep -n 'phase_prep_flatcar_and_ignition "\$SSH_TARGET"' | head -1 | cut -d: -f1)
    vm=$(printf '%s\n' "$body" | grep -n 'phase_provision_flatcar_vm "\$SSH_TARGET"' | head -1 | cut -d: -f1)

    if [[ -z "$gate" ]]; then
        hard_fail "the per-node loop never calls phase_recover_existing_vm; an existing VM is destroyed without anyone trying to start it"
        return
    fi
    local phase
    for phase in "prep_hyperv:$prep" "prep_flatcar_and_ignition:$ign" "provision_flatcar_vm:$vm"; do
        local name="${phase%%:*}" line="${phase##*:}"
        if [[ -z "$line" ]]; then
            hard_fail "phase_${name} is not called in the per-node loop — this check is reading the wrong file"
        elif (( gate > line )); then
            hard_fail "phase_recover_existing_vm runs AFTER phase_${name}; by then the node has already been destroyed"
        fi
    done
    pass "recovery gate runs before prep_hyperv, prep_flatcar_and_ignition and provision_flatcar_vm"
}

# The reservation race itself. Hyper-V releases a removed VM's memory
# asynchronously; the rebuild path used to Remove-VM, sleep two seconds, and
# then create and start the replacement. On a host near its ceiling that lost,
# with "Failed to finish reserving resources" and a VM left Off — which the
# phase then reported as a node that was never created.
validate_onprem_vm_recreate_waits_for_release() {
    section "Hyper-V memory reservation is waited for, not slept through"
    local script="$VALIDATE_ROOT/scripts/hybrid/provision-flatcar-worker.sh"
    local body
    body=$(grep -vE '^\s*#' "$script")

    if printf '%s\n' "$body" | grep -A2 'Remove-VM -Name \\\$vmName' | grep -q 'Start-Sleep -Seconds 2$'; then
        hard_fail "Remove-VM is followed by a fixed 2s sleep; poll Get-VM until the VM is gone instead"
        return
    fi
    if ! printf '%s\n' "$body" | grep -A6 'Remove-VM -Name \\\$vmName' | grep -q 'Get-VM -Name \\\$vmName'; then
        hard_fail "nothing confirms the removed VM is gone before the replacement is created"
        return
    fi
    if ! printf '%s\n' "$body" | grep -q 'Start-VM -Name \\\$vmName -ErrorAction Stop'; then
        hard_fail "Start-VM is issued without -ErrorAction Stop, so a failed start cannot be retried or reported"
        return
    fi
    pass "removal is polled to completion and Start-VM is retried"
}

# The capacity block computes a host memory ceiling and must USE it.
#
# $totalRamBytes and $reserveBytes were assigned at the top of the provisioning
# block and never read again: the "5 GB Host OS Reserve" in the capacity line was
# a string literal describing a reserve nothing enforced. A registry row asking
# for more RAM than the host has therefore reached New-VM unchecked, and failed
# later as "Failed to finish reserving resources ... OperationFailed,...StartVM"
# -- an error naming neither memory nor the value that caused it.
#
# That is the shape this checks for: a guard that is computed, described in the
# output, and not wired up. It is not observable at runtime on a host with enough
# memory, which is exactly why it survived.
validate_onprem_memory_ceiling_is_enforced() {
    section "host memory ceiling is enforced, not just computed"
    local script="$VALIDATE_ROOT/scripts/hybrid/provision-flatcar-worker.sh"
    local body
    body=$(grep -vE '^\s*#' "$script")

    # Assigned once, and read at least once more.
    local uses
    uses=$(printf '%s\n' "$body" | grep -c 'hostCeiling' || true)
    if [[ "$uses" -lt 2 ]]; then
        hard_fail "the host memory ceiling is computed but never applied; an over-large registry value reaches New-VM unchecked"
        return
    fi
    if ! printf '%s\n' "$body" | grep -q 'finalStartup -gt \\\$hostCeiling'; then
        hard_fail "startup memory is not compared against the host ceiling"
        return
    fi
    # Hyper-V requires min <= startup <= max; clamping startup alone is invalid.
    if ! printf '%s\n' "$body" | grep -q 'finalMin -gt \\\$finalStartup'; then
        hard_fail "startup can be clamped below MinimumBytes, which Set-VMMemory rejects"
        return
    fi
    # The reserve in the operator-facing line must be the one enforced, or the
    # message drifts from the code again -- which is how this arose.
    if printf '%s\n' "$body" | grep -q "GB Max, 5 GB Host OS Reserve"; then
        hard_fail "the capacity line hard-codes '5 GB Host OS Reserve' instead of printing the enforced value"
        return
    fi
    # Hyper-V accepts memory sizes only in multiples of 2 MB. The registry values
    # are whole GB and so aligned by accident; the clamped ceiling is host-total
    # minus 5 GB, and TotalPhysicalMemory is usable RAM rather than a round
    # number, so clamping produces an arbitrary byte count. New-VM rejects it
    # with "Failed to modify device 'Memory'" / InvalidParameter -- which names
    # the device, not the constraint, and every later cmdlet then fails
    # ObjectNotFound against a VM that was never created.
    if ! printf '%s\n' "$body" | grep -q 'memAlign'; then
        hard_fail "memory values are not aligned to a 2 MB boundary; a clamped ceiling is not a multiple of 2 MB and New-VM rejects it"
        return
    fi
    local aligned_lines
    aligned_lines=$(printf '%s\n' "$body" | grep -F -- '% \$memAlign' || true)
    local aligned=0 v
    for v in hostCeiling finalStartup finalMin finalMaxRam; do
        grep -qF -- "${v} % " <<< "$aligned_lines" && aligned=$((aligned + 1))
    done
    if [[ "$aligned" -lt 4 ]]; then
        hard_fail "only ${aligned}/4 memory values are 2 MB aligned (need hostCeiling, finalStartup, finalMin, finalMaxRam)"
        return
    fi

    pass "ceiling is applied to startup, min and max, all 2 MB aligned, and the reported reserve is the enforced one"
}

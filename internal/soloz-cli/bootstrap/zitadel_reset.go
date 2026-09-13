package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Zitadel's initialisation is not transactional, so a failed one cannot be
// retried -- it can only be started again from an empty database.
//
// cmd/setup writes to two places: the event stream, and projections.migrations,
// which is the record of what it has done. If it fails between them it leaves a
// database in which work has happened and nothing recorded it. Every retry then
// re-runs 03_default_instance, collides with the instance_domain constraint its
// predecessor wrote, fails with AlreadyExists, and -- by its own log -- "setup
// failed, skipping cleanup". The Job retries until backoff, identically, for
// ever.
//
// The comment in the chart's values assumed the opposite: "restartable by
// design -- cmd/setup runs cleanup on interruption so a retry can pick up where
// we left off". That is true of INTERRUPTION. It is not true of FAILURE, which
// takes the path that skips cleanup.
//
// This was found the slow way. The visible symptom was an ExternalSecret in a
// different namespace stalling for eleven minutes on every run, because Zitadel
// never came up, so iam-admin-pat was never created, so the identity token was
// never uploaded for ESO to deliver.
//
// So initialisation is treated as ATOMIC AT THE DATABASE, which is the same
// contract CNPG already states with `bootstrap: initdb` versus `recovery`, and
// the same one the database-recovery overlay applies to platform-db: a database
// whose initialisation did not complete is recreated, not repaired. That is safe
// precisely because incomplete initialisation means there is nothing to
// preserve.
const (
	zitadelDatabase = "zitadel"

	// The signature of the unrecoverable state, and nothing else.
	//
	// eventstore.events2 exists  -> setup wrote event data
	// projections.migrations absent -> and recorded none of it
	//
	// Both halves are required. A database with neither has not been touched and
	// needs no reset; one with both is a Zitadel that initialised properly and
	// must never be dropped.
	zitadelPartialInitQuery = `SELECT
	  (to_regclass('eventstore.events2') IS NOT NULL)
	  AND (to_regclass('projections.migrations') IS NULL)`
)

// resetZitadelIfInitIncomplete drops and recreates the Zitadel database when a
// previous bootstrap left it half-initialised.
//
// Gated on the phase, not only on the database. If boundary 04 completed then
// Zitadel initialised successfully at some point, and a later inconsistency is a
// running system's problem -- to be diagnosed, never silently dropped.
func (o *Orchestrator) resetZitadelIfInitIncomplete(ctx context.Context, kubeconfig string, phaseDone bool) error {
	if phaseDone {
		return nil
	}

	primary, err := o.cnpgPrimary(ctx, kubeconfig)
	if err != nil || primary == "" {
		// No database yet: a first bootstrap, with nothing to reset.
		return nil
	}

	out, err := o.psql(ctx, kubeconfig, primary, zitadelDatabase, zitadelPartialInitQuery)
	if err != nil {
		// The database may not exist yet. Not an error, and not a reason to stop.
		return nil
	}
	if strings.TrimSpace(out) != "t" {
		return nil
	}

	fmt.Println("[boundary04] Zitadel's database carries a half-finished initialisation.")
	fmt.Println("[boundary04]   eventstore written, projections.migrations absent -- so Zitadel")
	fmt.Println("[boundary04]   has done work it has no record of, and every setup retry fails")
	fmt.Println("[boundary04]   on a constraint its predecessor wrote. Recreating the database,")
	fmt.Println("[boundary04]   which is the only way back to a state setup can start from.")

	// Terminate first. DROP DATABASE fails while anything holds a connection,
	// and the crash-looping Zitadel reconnects between attempts.
	if _, err := o.psql(ctx, kubeconfig, primary, "postgres", fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %q`,
		zitadelDatabase)); err != nil {
		return fmt.Errorf("could not disconnect clients from the zitadel database: %w", err)
	}
	if _, err := o.psql(ctx, kubeconfig, primary, "postgres",
		"DROP DATABASE IF EXISTS "+zitadelDatabase); err != nil {
		return fmt.Errorf("could not drop the half-initialised zitadel database: %w", err)
	}
	// Recreated here rather than left to CNPG: the bootstrap SQL that creates it
	// runs once, at cluster initdb, and will not run again.
	if _, err := o.psql(ctx, kubeconfig, primary, "postgres",
		"CREATE DATABASE "+zitadelDatabase); err != nil {
		return fmt.Errorf("could not recreate the zitadel database: %w", err)
	}
	fmt.Println("[boundary04] ✓ Zitadel database recreated; setup will run against an empty one")
	return nil
}

// cnpgPrimary returns the name of the platform database's primary pod.
func (o *Orchestrator) cnpgPrimary(ctx context.Context, kubeconfig string) (string, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"get", "pods", "-n", "platform-data", "-l", "cnpg.io/instanceRole=primary",
		"-o", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// psql runs one statement in the primary and returns its output, unaligned and
// without headers so a caller can compare it directly.
func (o *Orchestrator) psql(ctx context.Context, kubeconfig, pod, database, statement string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"exec", "-n", "platform-data", pod, "-c", "postgres", "--",
		"psql", "-d", database, "-tAc", statement)
	out, err := cmd.Output()
	return string(out), err
}

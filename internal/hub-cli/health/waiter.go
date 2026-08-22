// Package health provides a unified framework for waiting on platform service
// readiness during bootstrap and Day-0 choreography.
//
// The package follows SOLID principles:
//
//   - Single Responsibility: each HealthChecker checks exactly one service.
//   - Open/Closed: new dependencies are added by writing a new HealthChecker,
//     never by modifying HealthWaiter.
//   - Liskov Substitution: any HealthChecker is interchangeable.
//   - Interface Segregation: HealthChecker has one method (Check).
//   - Dependency Inversion: HealthWaiter depends on the HealthChecker interface,
//     not on concrete service types.
//
// Two execution strategies are supported transparently behind the interface:
//
//  1. Kubernetes-clientset checkers (see clientset.go) for typed API access.
//  2. Kubectl-exec checkers (see kubectl.go) for resources that need shelling out
//     (CRD discovery, multi-namespace scans, JSONPath filtering).
//
// A HealthChecker picks its strategy internally and is called the same way.
package health

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HealthChecker is the contract for a single platform-service readiness check.
//
// Implementations should be stateless and safe for concurrent use.
// Each Check call performs a SINGLE poll, not a loop. The HealthWaiter
// owns the polling cadence.
type HealthChecker interface {
	// Name returns a short, human-readable identifier used in log output
	// (e.g., "Infisical", "CNPG cluster platform-db", "CAPI CRDs").
	Name() string
	// Check returns nil if the service is healthy, or a non-nil error
	// describing WHY it is not yet ready. The error message is shown to
	// operators and should be actionable.
	Check(ctx context.Context, kubeconfig string) error
}

// BootstrapReadinessCheck is the contract for an ordered set of HealthChecker
// instances representing the readiness profile of a specific bootstrap phase
// (e.g., "Day-0 data layer", "platform services").
//
// The same interface is used by Cobra commands and orchestrators to express
// "this phase is ready when all of these checks pass".
type BootstrapReadinessCheck interface {
	// PhaseName returns a human-readable identifier (e.g., "Data Layer",
	// "Platform Services").
	PhaseName() string
	// Checkers returns the ordered list of HealthChecker instances that
	// must all pass for this phase to be considered ready.
	Checkers() []HealthChecker
}

// HealthWaiter polls a sequence of HealthChecker instances until they all
// pass or the timeout elapses.
//
// Checks are executed in order. Each check must pass before the next runs.
// This preserves inter-service dependency ordering (CNPG → PgBouncer →
// Infisical, for example) without leaking the dependency into any one
// checker's implementation.
type HealthWaiter struct {
	// Checkers is the ordered list of checks to perform.
	Checkers []HealthChecker
	// Interval is the polling interval between checks. Default: 5s.
	Interval time.Duration
	// Timeout is the TOTAL budget for all checks combined. Default: 15m.
	Timeout time.Duration
	// OnCheckStart, if set, is called when each check begins. Useful for
	// emitting progress banners without coupling the waiter to console I/O.
	OnCheckStart func(check HealthChecker)
	// OnCheckPass, if set, is called after each successful check.
	OnCheckPass func(check HealthChecker)
}

// Wait polls the configured Checkers sequentially until they all pass or
// the total timeout elapses.
//
// Behavior:
//   - Each check is polled at the configured Interval until it returns nil.
//   - The first failing check blocks the sequence; later checks are skipped.
//   - If Timeout elapses before all checks pass, an error is returned
//     identifying which check failed and how long was waited.
//   - If ctx is cancelled, ctx.Err() is returned immediately.
//
// Returns nil on success, error on failure.
func (w *HealthWaiter) Wait(ctx context.Context, kubeconfig string) error {
	if w.Interval == 0 {
		w.Interval = 5 * time.Second
	}
	if w.Timeout == 0 {
		w.Timeout = 15 * time.Minute
	}

	deadline := time.Now().Add(w.Timeout)
	startedAt := time.Now()
	total := len(w.Checkers)

	// Announce the whole plan up front. On a hybrid hub these checks routinely
	// take 15+ minutes (images pull to a home-lab worker over a ~200ms link), and
	// without knowing how many stages there are — and which one is current — a
	// long wait is indistinguishable from a hang.
	if total > 1 {
		fmt.Printf("   %d checks to pass (budget %v):\n", total, w.Timeout)
		for i, c := range w.Checkers {
			fmt.Printf("     %d/%d %s\n", i+1, total, c.Name())
		}
	}

	for i, checker := range w.Checkers {
		if w.OnCheckStart != nil {
			w.OnCheckStart(checker)
		} else {
			fmt.Printf("   → [%d/%d] %s\n", i+1, total, checker.Name())
		}

		// Per-check deadline to keep error messages precise when Timeout
		// is large. This is the remaining budget for THIS check onward.
		checkStart := time.Now()
		lastReason := ""
		nextHeartbeat := checkStart.Add(heartbeatEvery)

		for {
			if err := ctx.Err(); err != nil {
				return err
			}

			err := checker.Check(ctx, kubeconfig)
			if err == nil {
				elapsed := time.Since(checkStart).Round(time.Second)
				if w.OnCheckPass != nil {
					w.OnCheckPass(checker)
				} else {
					fmt.Printf("   ✓ [%d/%d] %s (%v)\n", i+1, total, checker.Name(), elapsed)
					if rem := total - (i + 1); rem > 0 {
						fmt.Printf("     %d check(s) remaining: %s\n", rem, remainingNames(w.Checkers[i+1:]))
					}
				}
				break
			}

			// Surface WHY it is still waiting. The checker's error is the only
			// thing that distinguishes "pulling an image" from "CrashLoopBackOff",
			// and it used to be discarded on every poll.
			reason := condense(err.Error())
			if now := time.Now(); reason != lastReason || now.After(nextHeartbeat) {
				fmt.Printf("     … [%d/%d] %v elapsed — %s\n",
					i+1, total, time.Since(checkStart).Round(time.Second), reason)
				lastReason = reason
				nextHeartbeat = now.Add(heartbeatEvery)
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("health check %q failed after %v (was check %d/%d, total waited %v); last reason: %s",
					checker.Name(), time.Since(checkStart).Round(time.Second),
					i+1, total, time.Since(startedAt).Round(time.Second), reason)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.Interval):
			}
		}
	}

	if total > 1 {
		fmt.Printf("   ✓ all %d checks passed in %v\n", total, time.Since(startedAt).Round(time.Second))
	}
	return nil
}

// heartbeatEvery bounds how often an unchanged reason is repeated. Short enough
// that the operator sees the run is alive, long enough not to flood the log.
const heartbeatEvery = 30 * time.Second

func remainingNames(rest []HealthChecker) string {
	names := make([]string, 0, len(rest))
	for _, c := range rest {
		names = append(names, c.Name())
	}
	return strings.Join(names, ", ")
}

// condense reduces a checker error to one readable line. Kubernetes errors often
// carry embedded newlines and long resource dumps that would otherwise turn a
// progress line into a page.
func condense(msg string) string {
	msg = strings.TrimSpace(strings.ReplaceAll(msg, "\n", " "))
	msg = strings.Join(strings.Fields(msg), " ")
	const max = 160
	if len(msg) > max {
		return msg[:max] + "…"
	}
	return msg
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

func TestCallBudgetScalesWithSize(t *testing.T) {
	if got := callBudget(0); got != callBaseBudget {
		t.Fatalf("small object budget = %s, want %s", got, callBaseBudget)
	}
	if got := callBudget(200 << 20); got != callBaseBudget+200*callPerMiB {
		t.Fatalf("200 MiB budget = %s", got)
	}
}

func TestCallGivesEachOperationItsOwnDeadline(t *testing.T) {
	s := &Store{}
	var deadline time.Time
	var had bool
	err := s.call(context.Background(), "stat-object", "k", 0, func(ctx context.Context) error {
		deadline, had = ctx.Deadline()
		return nil
	})
	if err != nil || !had {
		t.Fatalf("no deadline on the call (err=%v)", err)
	}
	if left := time.Until(deadline); left <= 0 || left > callBaseBudget {
		t.Fatalf("deadline %s away, want within %s", left, callBaseBudget)
	}
}

func TestCallReturnsTheOperationsError(t *testing.T) {
	want := errors.New("boom")
	if err := (&Store{}).call(context.Background(), "put-object", "k", 0, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}

func TestCallStopsWhenTheOuterContextEnds(t *testing.T) {
	outer, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&Store{}).call(outer, "stat-object", "k", 0, func(ctx context.Context) error { return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestNotFoundIsNotAFailure(t *testing.T) {
	if !isNotFound(minio.ErrorResponse{Code: "NoSuchKey"}) {
		t.Fatal("NoSuchKey not recognised")
	}
	if isNotFound(errors.New("timeout awaiting response headers")) {
		t.Fatal("a timeout read as not-found")
	}
}

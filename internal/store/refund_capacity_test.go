package store_test

import (
	"context"
	"llm-refund/internal/store"
	"testing"
)

func TestMotherCapacityRequeuesWithBoundedBudgetThenReleases(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	ctx := context.Background()
	cfg, _ := s.RefundSettings(ctx)
	cfg.MotherFailover.MaxSwitches = 1
	if err := s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	submitRequests(t, s, u, 1)
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	d := store.RefundDiagnostic{Code: "mother_capacity", HTTPStatus: 200}
	if err = s.FinishRefundAttempt(ctx, j, d); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Refund(ctx, u.ID, j.ID)
	if r.Status != "queued" || r.FeeStatus != "frozen" {
		t.Fatal(r)
	}
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.ID != j.ID {
		t.Fatal("capacity recovery never rechecked", err)
	}
	if err = s.FinishRefundAttempt(ctx, next, d); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Refund(ctx, u.ID, j.ID)
	if r.Status != "failed" || r.FeeStatus != "released" {
		t.Fatal(r)
	}
	var enabled bool
	s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_mothers WHERE id=$1", j.Mother.ID).Scan(&enabled)
	if enabled {
		t.Fatal("exhausted full mother still receiving orders")
	}
	var freezes, releases int
	s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE kind='freeze'),count(*) FILTER(WHERE kind='release') FROM ledger WHERE refund_id=$1", j.ID).Scan(&freezes, &releases)
	if freezes != 1 || releases != 1 {
		t.Fatal("financial transition duplicated", freezes, releases)
	}
}

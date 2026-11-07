package store_test

import (
	"context"
	"errors"
	"testing"

	"llm-refund/internal/store"
)

func TestDeleteMotherWithRunningTaskPreservesCleanupAndTeamSeriality(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	enableRecovery(t, s, a)
	ctx := context.Background()
	mothers, _ := s.RefundMothers(ctx)
	m := mothers[0]
	submitRequests(t, s, u, 2)
	j, _ := s.ClaimRefund(ctx)
	prepareExport(t, s, j)
	checkpoint(t, s, j, "merge_v2_accepted")
	var before string
	s.Pool.QueryRow(ctx, "SELECT to_jsonb(e)::text FROM refund_execution e WHERE refund_id=$1", j.ID).Scan(&before)
	if err := s.DeleteRefundResource(ctx, u.ID, "mother", m.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member can delete mother", err)
	}
	if err := s.DeleteRefundResource(ctx, a.ID, "mother", m.ID); err != nil {
		t.Fatal("running task blocked removal", err)
	}
	if err := s.DeleteRefundResource(ctx, a.ID, "mother", m.ID); err != nil {
		t.Fatal("repeated deletion failed", err)
	}
	var after string
	s.Pool.QueryRow(ctx, "SELECT to_jsonb(e)::text FROM refund_execution e WHERE refund_id=$1", j.ID).Scan(&after)
	if before != after {
		t.Fatal("deletion changed order execution")
	}
	mothers, _ = s.RefundMothers(ctx)
	if len(mothers) != 0 {
		t.Fatal("deleted mother still listed")
	}
	if _, err := s.PrepareMotherProbe(ctx, a.ID, store.RefundMother{ID: m.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted mother still probeable", err)
	}
	if _, err := s.SaveRefundMother(ctx, a.ID, m); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stale edit resurrected deleted mother", err)
	}
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("deleted mother assigned new work", err)
	}
	// Readding the same email/team is allowed, but its existing team task remains serial.
	m.ID = ""
	m.AccessToken = "new-configuration-token"
	newID, err := s.SaveRefundMother(ctx, a.ID, m)
	if err != nil {
		t.Fatal("readding removed team blocked", err)
	}
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("readded team stole active seat", err)
	}
	if err := s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "mother_network", NetworkCode: 28}); err != nil {
		t.Fatal(err)
	}
	clean := claimCleanup(t, s, j)
	if clean.Mother.ID != j.Mother.ID || clean.Mother.AccessToken != j.Mother.AccessToken {
		t.Fatal("cleanup lost removed resource context")
	}
	checkpoint(t, s, clean, "removing")
	checkpoint(t, s, clean, "completed")
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.ID == j.ID || next.Mother.ID != newID {
		t.Fatal("replacement did not receive next queued order", err)
	}
	r, _ := s.Refund(ctx, u.ID, j.ID)
	if r.Status != "succeeded" || r.FeeStatus != "settled" {
		t.Fatal(r)
	}
}

func TestDeleteMotherWithHistoryKeepsLedgerAndExecution(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	ctx := context.Background()
	submitRequests(t, s, u, 1)
	j, _ := s.ClaimRefund(ctx)
	prepareExport(t, s, j)
	checkpoint(t, s, j, "merge_v2_accepted")
	checkpoint(t, s, j, "removing")
	checkpoint(t, s, j, "completed")
	var before, after string
	snapshot := "SELECT json_build_object('order',(SELECT to_jsonb(r)-'credentials' FROM refunds r WHERE id=$1),'execution',(SELECT to_jsonb(e) FROM refund_execution e WHERE refund_id=$1),'ledger',(SELECT json_agg(l ORDER BY l.id) FROM ledger l WHERE refund_id=$1))::text"
	s.Pool.QueryRow(ctx, snapshot, j.ID).Scan(&before)
	if err := s.DeleteRefundResource(ctx, a.ID, "mother", j.Mother.ID); err != nil {
		t.Fatal("historical reference blocked deletion", err)
	}
	s.Pool.QueryRow(ctx, snapshot, j.ID).Scan(&after)
	if before != after {
		t.Fatal("historical financial or execution evidence changed")
	}
	view, err := s.AdminRefundTimeline(ctx, j.ID)
	if err != nil || view == nil {
		t.Fatal("historical timeline unavailable", err)
	}
}

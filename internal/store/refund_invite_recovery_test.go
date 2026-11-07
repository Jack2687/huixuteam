package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"llm-refund/internal/store"
)

func inviteAuthFailure() store.RefundDiagnostic {
	return store.RefundDiagnostic{Code: "mother_invite_auth", HTTPStatus: 401, Evidence: &store.RefundEvidence{Stage: "invite_send", Side: "mother", Event: "failure"}}
}

func claimCleanup(t *testing.T, s *store.Store, j *store.RefundJob) *store.RefundJob {
	t.Helper()
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(context.Background())
	if err != nil || next == nil || next.ID != j.ID || next.RecoveryMode != "cleanup" {
		t.Fatalf("expected original cleanup, job=%v err=%v", next, err)
	}
	return next
}

func TestInviteAuthCleanupRequeuesOriginalFeeAndSerializes(t *testing.T) {
	s, a, u := executionFixture(t, 2)
	enableRecovery(t, s, a)
	ctx := context.Background()
	submitRequests(t, s, u, 2)
	j, _ := s.ClaimRefund(ctx)
	busy, _ := s.ClaimRefund(ctx)
	checkpoint(t, s, j, "inviting")
	if err := s.FinishRefundAttempt(ctx, j, inviteAuthFailure()); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Refund(ctx, u.ID, j.ID)
	if r.Status != "processing" || r.FeeStatus != "frozen" {
		t.Fatal("401 must not release before cleanup", r)
	}
	clean := claimCleanup(t, s, j)
	// A transient cleanup error must neither switch nor lose the original cause.
	if err := s.FinishRefundAttempt(ctx, clean, store.RefundDiagnostic{Code: "mother_network", NetworkCode: 28}); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Refund(ctx, u.ID, j.ID)
	if r.Status != "processing" {
		t.Fatal("switched without confirmed cleanup", r)
	}
	clean = claimCleanup(t, s, j)
	checkpoint(t, s, clean, "cleanup_started")
	checkpoint(t, s, clean, "cleanup_completed")
	r, _ = s.Refund(ctx, u.ID, j.ID)
	if r.Status != "queued" || r.FeeStatus != "frozen" {
		t.Fatal(r)
	}
	var enabled bool
	s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_mothers WHERE id=$1", j.Mother.ID).Scan(&enabled)
	if enabled {
		t.Fatal("failed mother still accepting new orders")
	}
	if err := s.SaveResourceCheck(ctx, a.ID, "mother", j.Mother.ID, time.Now().Add(time.Second), store.ResourceCheck{Valid: true, Code: "ok", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_mothers WHERE id=$1", j.Mother.ID).Scan(&enabled)
	if enabled {
		t.Fatal("read-only probe reenabled invitation permission")
	}
	if err := s.RefundCheckpoint(ctx, clean, store.Checkpoint{Method: "cleanup_completed"}); !errors.Is(err, store.ErrState) {
		t.Fatal("stale cleanup accepted", err)
	}
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("switch delay/busy replacement ignored", err)
	}
	recoveryDue(t, s, j.ID)
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("busy replacement stolen", err)
	}
	prepareExport(t, s, busy)
	checkpoint(t, s, busy, "merge_v2_accepted")
	checkpoint(t, s, busy, "removing")
	checkpoint(t, s, busy, "completed")
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.ID != j.ID || next.Mother.ID != busy.Mother.ID || next.Step != "assigned" || next.ChildProxy != j.ChildProxy {
		t.Fatal("original request/context not resumed", err)
	}
	prepareExport(t, s, next)
	checkpoint(t, s, next, "merge_v2_accepted")
	checkpoint(t, s, next, "removing")
	checkpoint(t, s, next, "completed")
	var freezes, settlements, releases int
	s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE kind='freeze'),count(*) FILTER(WHERE kind='settlement'),count(*) FILTER(WHERE kind='release') FROM ledger WHERE refund_id=$1", j.ID).Scan(&freezes, &settlements, &releases)
	if freezes != 1 || settlements != 1 || releases != 0 {
		t.Fatal(freezes, settlements, releases)
	}
}

func TestInviteAuthCleanupHonorsCurrentSwitchLimit(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "disabled"}[disable], func(t *testing.T) {
			s, a, u := executionFixture(t, 2)
			enableRecovery(t, s, a)
			ctx := context.Background()
			submitRequests(t, s, u, 1)
			j, _ := s.ClaimRefund(ctx)
			checkpoint(t, s, j, "inviting")
			if err := s.FinishRefundAttempt(ctx, j, inviteAuthFailure()); err != nil {
				t.Fatal(err)
			}
			clean := claimCleanup(t, s, j)
			cfg, _ := s.RefundSettings(ctx)
			cfg.MotherFailover.Enabled = !disable
			cfg.MotherFailover.MaxSwitches = 1
			if err := s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
				t.Fatal(err)
			}
			s.Pool.Exec(ctx, "UPDATE refund_execution SET mother_switch_count=1 WHERE refund_id=$1", j.ID)
			checkpoint(t, s, clean, "cleanup_completed")
			r, _ := s.Refund(ctx, u.ID, j.ID)
			if r.Status != "failed" || r.FeeStatus != "released" {
				t.Fatal(r)
			}
		})
	}
}

func TestInviteAuthCleanupDoesNotPauseReplacedCredentials(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	enableRecovery(t, s, a)
	ctx := context.Background()
	submitRequests(t, s, u, 1)
	j, _ := s.ClaimRefund(ctx)
	checkpoint(t, s, j, "inviting")
	if err := s.FinishRefundAttempt(ctx, j, inviteAuthFailure()); err != nil {
		t.Fatal(err)
	}
	mothers, _ := s.RefundMothers(ctx)
	m := mothers[0]
	m.AccessToken = "new-mother-credential"
	if _, err := s.SaveRefundMother(ctx, a.ID, m); err != nil {
		t.Fatal(err)
	}
	clean := claimCleanup(t, s, j)
	checkpoint(t, s, clean, "cleanup_completed")
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.ID != j.ID || next.Mother.ID != j.Mother.ID || next.MotherAttemptKey == j.MotherAttemptKey {
		t.Fatal("new credential paused or excluded", err)
	}
}

func TestInviteAuthCleanupNeverSwitchesOtherFailures(t *testing.T) {
	for _, kind := range []string{"read401", "invite403", "child401", "login", "worker", "missing_resource_evidence"} {
		t.Run(kind, func(t *testing.T) {
			s, a, u := executionFixture(t, 2)
			enableRecovery(t, s, a)
			ctx := context.Background()
			submitRequests(t, s, u, 1)
			j, _ := s.ClaimRefund(ctx)
			checkpoint(t, s, j, "inviting")
			d := inviteAuthFailure()
			switch kind {
			case "read401":
				d.Code = "mother_http"
				d.Evidence.Stage = "members_read"
			case "invite403":
				d.HTTPStatus = 403
			case "child401":
				d.Evidence.Side = "child"
			case "login":
				checkpoint(t, s, j, "invited")
				checkpoint(t, s, j, "login")
			case "worker":
				d.Code = "worker_interrupted"
			case "missing_resource_evidence":
				j.MotherAttemptKey = ""
			}
			if err := s.FinishRefundAttempt(ctx, j, d); err != nil {
				t.Fatal(err)
			}
			clean := claimCleanup(t, s, j)
			checkpoint(t, s, clean, "cleanup_completed")
			r, _ := s.Refund(ctx, u.ID, j.ID)
			if r.Status != "failed" || r.FeeStatus != "released" {
				t.Fatal("unrelated failure switched", r)
			}
		})
	}
}

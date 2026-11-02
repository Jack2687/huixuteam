package store_test

import (
	"context"
	"fmt"
	"llm-refund/internal/store"
	"testing"
	"time"
)

func TestLoginTransientHTTPDelayBudgetAndCleanup(t *testing.T) {
	for _, code := range []int{500, 429} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			ctx := context.Background()
			submitRequests(t, s, u, 1)
			j, _ := s.ClaimRefund(ctx)
			checkpoint(t, s, j, "inviting")
			checkpoint(t, s, j, "invited")
			checkpoint(t, s, j, "login")
			reason := "login_upstream_unavailable"
			if code == 429 {
				reason = "chatgpt_rate_limited"
			}
			d := store.RefundDiagnostic{Code: "login_failed", HTTPStatus: code, Retryable: true,
				Evidence: &store.RefundEvidence{Side: "child", Stage: "mfa_verify", Reason: reason, RetryAfterSeconds: 120}}
			for n := 0; n < 4; n++ {
				before := time.Now()
				if err := s.FinishRefundAttempt(ctx, j, d); err != nil {
					t.Fatal(err)
				}
				v, _ := s.AdminRefundRecovery(ctx, j.ID)
				if n < 3 && (v.NextAt == nil || v.NextAt.Before(before.Add(119*time.Second))) {
					t.Fatal("Retry-After was not respected", v)
				}
				if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
					t.Fatal("retry delay skipped", err)
				}
				recoveryDue(t, s, j.ID)
				next, err := s.ClaimRefund(ctx)
				if err != nil || next == nil || next.ID != j.ID || next.Mother.ID != j.Mother.ID || (n < 3 && next.ChildProxy != j.ChildProxy) {
					t.Fatal("recovery context changed", err)
				}
				j = next
			}
			if j.RecoveryMode != "cleanup" {
				t.Fatal("login retries exceeded the configured budget")
			}
			checkpoint(t, s, j, "cleanup_started")
			checkpoint(t, s, j, "cleanup_completed")
			r, _ := s.Refund(ctx, u.ID, j.ID)
			if r.Status != "failed" || r.FeeStatus != "released" {
				t.Fatal(r)
			}
		})
	}
}

func TestTransientMotherFailuresCannotExcludeWholePoolForever(t *testing.T) {
	for _, size := range []int{1, 2} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, _, u := executionFixture(t, size)
			ctx := context.Background()
			submitRequests(t, s, u, 1)
			j, _ := s.ClaimRefund(ctx)
			id := j.ID
			for n := 0; n < 4; n++ {
				previousMother, before := j.Mother.ID, time.Now()
				if err := s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "mother_preflight_failed", HTTPStatus: 500,
					Evidence: &store.RefundEvidence{Side: "mother", RetryAfterSeconds: 90}}); err != nil {
					t.Fatal(err)
				}
				if n == 3 {
					break
				}
				v, _ := s.AdminRefundRecovery(ctx, id)
				if v.NextAt == nil || v.NextAt.Before(before.Add(89*time.Second)) {
					t.Fatal("mother cooldown ignored", v)
				}
				if early, err := s.ClaimRefund(ctx); err != nil || early != nil {
					t.Fatal("mother cooldown skipped", err)
				}
				recoveryDue(t, s, id)
				next, err := s.ClaimRefund(ctx)
				if err != nil || next == nil || next.ID != id || next.Step != "assigned" {
					t.Fatal("temporary failures stranded the order", err)
				}
				if size == 2 && next.Mother.ID == previousMother {
					t.Fatal("did not prefer another available mother")
				}
				j = next
			}
			r, _ := s.Refund(ctx, u.ID, id)
			if r.Status != "failed" || r.FeeStatus != "released" {
				t.Fatal("switch budget not exhausted", r)
			}
			var freezes, releases, settles int
			s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE kind='freeze'),count(*) FILTER(WHERE kind='release'),count(*) FILTER(WHERE kind='settlement') FROM ledger WHERE refund_id=$1", id).Scan(&freezes, &releases, &settles)
			if freezes != 1 || releases != 1 || settles != 0 {
				t.Fatal("incorrect accounting", freezes, releases, settles)
			}
		})
	}
}

func TestTemporaryFailureDoesNotReenablePermanentlyRejectedMother(t *testing.T) {
	s, _, u := executionFixture(t, 2)
	ctx := context.Background()
	submitRequests(t, s, u, 1)
	j, _ := s.ClaimRefund(ctx)
	rejected := j.Mother.ID
	if err := s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "mother_preflight_failed", HTTPStatus: 403}); err != nil {
		t.Fatal(err)
	}
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.Mother.ID == rejected {
		t.Fatal("rejected mother selected", err)
	}
	if err := s.FinishRefundAttempt(ctx, next, store.RefundDiagnostic{Code: "mother_preflight_failed", HTTPStatus: 500}); err != nil {
		t.Fatal(err)
	}
	recoveryDue(t, s, j.ID)
	after, err := s.ClaimRefund(ctx)
	if err != nil || after == nil || after.Mother.ID == rejected {
		t.Fatal("temporary error cleared a permanent exclusion", err)
	}
}

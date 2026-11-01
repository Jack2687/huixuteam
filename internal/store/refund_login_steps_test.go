package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
)

func TestLoginRecoveryHonorsConfiguredDelayAndLimit(t *testing.T) {
	for _, code := range []string{"team_session_expired", "extra_verification"} {
		t.Run(code, func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			ctx := context.Background()
			cfg, err := s.RefundSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Recovery.MaxRetries = 2
			cfg.RetrySeconds = 37
			if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
				t.Fatal(err)
			}
			submitRequests(t, s, u, 1)
			j, err := s.ClaimRefund(ctx)
			if err != nil || j == nil {
				t.Fatal(j, err)
			}
			checkpoint(t, s, j, "inviting")
			checkpoint(t, s, j, "invited")
			original := j
			for attempt := 0; attempt <= 2; attempt++ {
				checkpoint(t, s, j, "login")
				d := store.RefundDiagnostic{Code: code, Retryable: code != "extra_verification", HTTPStatus: 302,
					Evidence: &store.RefundEvidence{Stage: "authorize", Side: "child", Reason: "team_session_expired", LoginPage: "login", ContinueKind: "login"}}
				if err = s.FinishRefundAttempt(ctx, j, d); err != nil {
					t.Fatal(err)
				}
				r, err := s.Refund(ctx, u.ID, j.ID)
				if err != nil || r.FeeStatus != "frozen" {
					t.Fatal(r, err)
				}
				if strings.Contains(r.Message, "再次尝试") || strings.Contains(r.Message, "已释放") {
					t.Fatal("terminal prompt before retries exhausted", r.Message)
				}
				if _, _, err = s.CreateRefunds(ctx, u.ID, "duplicate-while-retrying", []refundprovider.Account{j.Account}); !errors.Is(err, store.ErrDuplicate) {
					t.Fatal("duplicate permitted before release", err)
				}
				if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
					t.Fatal("interval ignored", next, err)
				}
				var delay float64
				var retries int
				if err = s.Pool.QueryRow(ctx, "SELECT EXTRACT(EPOCH FROM next_attempt_at-now()),retry_count FROM refund_execution WHERE refund_id=$1", j.ID).Scan(&delay, &retries); err != nil {
					t.Fatal(err)
				}
				if retries != attempt {
					t.Fatal("retry count", retries, attempt)
				}
				if attempt < 2 && (delay < 30 || delay > 37) {
					t.Fatal("configured delay ignored", delay)
				}
				recoveryDue(t, s, j.ID)
				next, err := s.ClaimRefund(ctx)
				if err != nil || next == nil {
					t.Fatal(next, err)
				}
				mode := "retry"
				if attempt == 2 {
					mode = "cleanup"
				}
				if next.RecoveryMode != mode || next.ID != original.ID || next.Mother.ID != original.Mother.ID || (mode == "retry" && next.ChildProxy != original.ChildProxy) {
					t.Fatal("recovery context changed", next.RecoveryMode)
				}
				j = next
			}
			checkpoint(t, s, j, "cleanup_started")
			checkpoint(t, s, j, "cleanup_completed")
			assertAutoRelease(t, s, j)
			if code == "extra_verification" {
				r, err := s.Refund(ctx, u.ID, j.ID)
				if err != nil || r.Message != "登录需要额外验证码，服务费已释放，可再次尝试。" {
					t.Fatal(r, err)
				}
			}
			if err = s.FinishRefundAttempt(ctx, original, store.RefundDiagnostic{Code: code, Retryable: code != "extra_verification"}); err != nil {
				t.Fatal(err)
			}
			assertAutoRelease(t, s, j)
		})
	}
}

func TestExtraVerificationZeroRetryBudgetCleansBeforeRelease(t *testing.T) {
	for _, cleanup := range []bool{true, false} {
		t.Run(map[bool]string{true: "cleanup", false: "manual"}[cleanup], func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			ctx := context.Background()
			cfg, _ := s.RefundSettings(ctx)
			cfg.Recovery.CleanupFailed = cleanup
			cfg.Recovery.MaxRetries = 0
			if err := s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
				t.Fatal(err)
			}
			submitRequests(t, s, u, 1)
			j, err := s.ClaimRefund(ctx)
			if err != nil || j == nil {
				t.Fatal(j, err)
			}
			checkpoint(t, s, j, "inviting")
			checkpoint(t, s, j, "invited")
			checkpoint(t, s, j, "login")
			// A worker flag cannot override a zero retry budget.
			if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "extra_verification", Retryable: true}); err != nil {
				t.Fatal(err)
			}
			r, _ := s.Refund(ctx, u.ID, j.ID)
			if cleanup && r.FeeStatus != "frozen" {
				t.Fatal("released before cleanup", r)
			}
			if !cleanup {
				if r.Status != "failed" || r.FeeStatus != "released" {
					t.Fatal(r)
				}
				if strings.Contains(r.Message, "再次尝试") {
					t.Fatal("cleanup was not confirmed", r.Message)
				}
				if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
					t.Fatal(next, err)
				}
				return
			}
			if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
				t.Fatal("cleanup interval ignored", next, err)
			}
			recoveryDue(t, s, j.ID)
			next, err := s.ClaimRefund(ctx)
			if err != nil || next == nil || next.RecoveryMode != "cleanup" {
				t.Fatal(next, err)
			}
			checkpoint(t, s, next, "cleanup_started")
			checkpoint(t, s, next, "cleanup_completed")
			// Late results must not release twice or restart the original login.
			if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "extra_verification"}); err != nil {
				t.Fatal(err)
			}
			r, _ = s.Refund(ctx, u.ID, j.ID)
			e, _ := s.RefundExecution(ctx, j.ID)
			var releases, settlements, retries int
			if err = s.Pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE kind='release'), count(*) FILTER (WHERE kind='settlement') FROM ledger WHERE refund_id=$1", j.ID).Scan(&releases, &settlements); err != nil {
				t.Fatal(err)
			}
			if err = s.Pool.QueryRow(ctx, "SELECT retry_count FROM refund_execution WHERE refund_id=$1", j.ID).Scan(&retries); err != nil {
				t.Fatal(err)
			}
			if r.Status != "failed" || r.FeeStatus != "released" || !e.Released || releases != 1 || settlements != 0 || retries != 0 {
				t.Fatal(r, releases, settlements, retries)
			}
		})
	}
}

func TestExtraVerificationReloginCanCompleteWithoutEarlyRelease(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	enableRecovery(t, s, a)
	submitRequests(t, s, u, 1)
	ctx := context.Background()
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	checkpoint(t, s, j, "inviting")
	checkpoint(t, s, j, "invited")
	checkpoint(t, s, j, "login")
	if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "extra_verification"}); err != nil {
		t.Fatal(err)
	}
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("retry ignored delay", next, err)
	}
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.Step != "login" || next.RecoveryMode != "retry" {
		t.Fatal(next, err)
	}
	checkpoint(t, s, next, "login")
	if err = s.RefundCheckpoint(ctx, next, store.Checkpoint{Method: "merge_v2_ready", TeamID: next.Mother.TeamID, UserID: "child-user"}); err != nil {
		t.Fatal(err)
	}
	checkpoint(t, s, next, "merge_v2_submitting")
	checkpoint(t, s, next, "merge_v2_accepted")
	checkpoint(t, s, next, "removing")
	checkpoint(t, s, next, "completed")
	// A late challenge result from the original lease must not undo success.
	if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "extra_verification"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Refund(ctx, u.ID, j.ID)
	if err != nil || r.Status != "succeeded" || r.FeeStatus != "settled" {
		t.Fatal(r, err)
	}
	var freezes, releases, settlements, invites int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE kind='freeze'),count(*) FILTER(WHERE kind='release'),count(*) FILTER(WHERE kind='settlement') FROM ledger WHERE refund_id=$1", j.ID).Scan(&freezes, &releases, &settlements); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM refund_events WHERE refund_id=$1 AND message='正在发送团队邀请。'", j.ID).Scan(&invites); err != nil {
		t.Fatal(err)
	}
	if freezes != 1 || releases != 0 || settlements != 1 || invites != 1 {
		t.Fatal(freezes, releases, settlements, invites)
	}
}

func TestExtraVerificationDoesNotRestartAcceptedOrDisabledRecovery(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery_disabled", true: "already_accepted"}[accepted], func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			ctx := context.Background()
			if !accepted {
				cfg, err := s.RefundSettings(ctx)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Recovery.Enabled = false
				if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
					t.Fatal(err)
				}
			}
			submitRequests(t, s, u, 1)
			j, err := s.ClaimRefund(ctx)
			if err != nil || j == nil {
				t.Fatal(j, err)
			}
			if accepted {
				prepareExport(t, s, j)
				checkpoint(t, s, j, "merge_v2_accepted")
			} else {
				checkpoint(t, s, j, "inviting")
				checkpoint(t, s, j, "invited")
				checkpoint(t, s, j, "login")
			}
			if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "extra_verification", Retryable: true}); err != nil {
				t.Fatal(err)
			}
			if !accepted {
				assertAutoRelease(t, s, j)
				return
			}
			recoveryDue(t, s, j.ID)
			next, err := s.ClaimRefund(ctx)
			if err != nil || next == nil || next.RecoveryMode != "cleanup" || next.Step != "accepted" {
				t.Fatal(next, err)
			}
			if err = s.RefundCheckpoint(ctx, next, store.Checkpoint{Method: "login"}); !errors.Is(err, store.ErrState) {
				t.Fatal("accepted job allowed login", err)
			}
		})
	}
}

func TestLoginFailureAfterSubmissionStillOnlyVerifies(t *testing.T) {
	for _, code := range []string{"login_response_incomplete", "team_session_expired", "extra_verification"} {
		t.Run(code, func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			submitRequests(t, s, u, 1)
			ctx := context.Background()
			j, _ := s.ClaimRefund(ctx)
			prepareExport(t, s, j)
			if err := s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: code, Retryable: true}); err != nil {
				t.Fatal(err)
			}
			recoveryDue(t, s, j.ID)
			next, err := s.ClaimRefund(ctx)
			if err != nil || next == nil || next.RecoveryMode != "verify" || next.Step != "submitting" {
				t.Fatal(next, err)
			}
		})
	}
}

func TestLoginStepEvidenceAllowlist(t *testing.T) {
	for _, reason := range []string{"team_session_expired", "personal_session_conflict"} {
		e := store.RefundEvidence{Reason: reason, LoginPage: "login", ContinueKind: "login"}.Sanitized()
		if e.Reason != reason || e.LoginPage != "login" || e.ContinueKind != "login" {
			t.Fatal(e)
		}
	}
	v := store.RefundEvidence{LoginPage: "email_otp_verification", ContinueKind: "email", VerificationMode: "passwordless_login", Reason: "extra_verification"}.Sanitized()
	if v.LoginPage != "email_otp_verification" || v.Reason != "extra_verification" {
		t.Fatal(v)
	}
	v = store.RefundEvidence{LoginPage: "private-token", ContinueKind: "https://private.invalid", VerificationMode: "private-mode"}.Sanitized()
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "private") {
		t.Fatal("untrusted diagnostic retained")
	}
}

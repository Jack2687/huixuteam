package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
)

func TestConfirmedLoginFailureMessages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic store.RefundDiagnostic
		message    string
	}{
		{"verification", store.RefundDiagnostic{Code: "extra_verification"}, "登录需要额外验证码，服务费已释放，可再次尝试。"},
		{"deactivated", store.RefundDiagnostic{Code: "account_unavailable", HTTPStatus: 403, Evidence: &store.RefundEvidence{Side: "child", Stage: "mfa_verify", Reason: "account_deactivated"}}, "账号已停用，本次未能办理，服务费已释放。"},
		{"generic403", store.RefundDiagnostic{Code: "account_unavailable", HTTPStatus: 403}, "本次服务未完成，冻结服务费已释放。"},
		{"ordinary_mfa", store.RefundDiagnostic{Code: "login_failed", Evidence: &store.RefundEvidence{Side: "child", LoginPage: "mfa_challenge"}}, "本次服务未完成，冻结服务费已释放。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			// Exercise final messages after the configured login budget is exhausted.
			cfg, err := s.RefundSettings(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cfg.Recovery.MaxRetries = 0
			if err = s.SaveRefundSettings(context.Background(), a.ID, cfg); err != nil {
				t.Fatal(err)
			}
			submitRequests(t, s, u, 1)
			ctx := context.Background()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			j, err := s.ClaimRefund(ctx)
			must(err)
			for _, method := range []string{"inviting", "invited", "login"} {
				checkpoint(t, s, j, method)
			}
			// An earlier attempt's challenge must not label this login.
			_, err = s.Pool.Exec(ctx, `INSERT INTO refund_execution_diagnostics(refund_id,diagnostic,created_at)
 VALUES($1,'{"code":"extra_verification"}',now()-interval '1 hour')`, j.ID)
			must(err)
			must(s.FinishRefundAttempt(ctx, j, tc.diagnostic))
			r, err := s.Refund(ctx, u.ID, j.ID)
			must(err)
			if r.FeeStatus != "frozen" || strings.Contains(r.Message, "再次尝试") || strings.Contains(r.Message, "已停用") {
				t.Fatal("terminal message before cleanup", r)
			}
			if _, _, err = s.CreateRefunds(ctx, u.ID, "before-cleanup", []refundprovider.Account{j.Account}); !errors.Is(err, store.ErrDuplicate) {
				t.Fatal("duplicate allowed before release", err)
			}
			recoveryDue(t, s, j.ID)
			cleanup, err := s.ClaimRefund(ctx)
			must(err)
			if cleanup == nil || cleanup.RecoveryMode != "cleanup" {
				t.Fatal(cleanup)
			}
			checkpoint(t, s, cleanup, "cleanup_started")
			// Cleanup can fail and restart; last_diagnostic no longer has the login cause.
			must(s.FinishRefundAttempt(ctx, cleanup, store.RefundDiagnostic{Code: "cleanup_invite_pending"}))
			recoveryDue(t, s, j.ID)
			cleanup, err = s.ClaimRefund(ctx)
			must(err)
			checkpoint(t, s, cleanup, "cleanup_started")
			checkpoint(t, s, cleanup, "cleanup_completed")
			must(s.FinishRefundAttempt(ctx, j, tc.diagnostic)) // stale result must be fenced
			r, err = s.Refund(ctx, u.ID, j.ID)
			must(err)
			if r.Message != tc.message {
				t.Fatal(r.Message)
			}
			assertAutoRelease(t, s, cleanup)
			events, err := s.Events(ctx, u.ID, j.ID)
			must(err)
			found := false
			for _, event := range events {
				if event.Message == tc.message {
					found = true
				}
			}
			if !found {
				t.Fatal("missing public completion event")
			}
			timeline, err := s.AdminRefundTimeline(ctx, j.ID)
			must(err)
			if len(timeline.Items) == 0 || timeline.Items[len(timeline.Items)-1].Stage != "结" {
				t.Fatal("missing terminal timeline", timeline)
			}
			ids, _, err := s.CreateRefunds(ctx, u.ID, "after-cleanup", []refundprovider.Account{j.Account})
			must(err)
			if len(ids) != 1 || ids[0] == j.ID {
				t.Fatal("fresh request unavailable", ids)
			}
		})
	}
}

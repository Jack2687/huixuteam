package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"llm-refund/internal/store"
)

func passwordCheckpoint() store.Checkpoint {
	return store.Checkpoint{Method: "password_failed", Diagnostic: &store.RefundDiagnostic{Code: "password_verification_failed", HTTPStatus: 401, Evidence: &store.RefundEvidence{Stage: "password", Side: "child", Event: "failure"}}}
}

func TestPassword401CleanupImmediateDurableAndReleasesOnce(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{true: "recovery_on", false: "recovery_off"}[enabled], func(t *testing.T) {
			s, a, u := executionFixture(t, 1)
			enableRecovery(t, s, a)
			ctx := context.Background()
			cfg, _ := s.RefundSettings(ctx)
			childProxy, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "Child only", URL: "http://child.invalid:8081", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			cfg.ChildProxyID = childProxy
			cfg.Recovery.Enabled = enabled
			cfg.Recovery.CleanupFailed = false
			if err := s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
				t.Fatal(err)
			}
			submitRequests(t, s, u, 2)
			j, err := s.ClaimRefund(ctx)
			if err != nil || j == nil {
				t.Fatal(err)
			}
			checkpoint(t, s, j, "inviting")
			checkpoint(t, s, j, "invited")
			checkpoint(t, s, j, "login")
			if err = s.RefundCheckpoint(ctx, j, passwordCheckpoint()); err != nil {
				t.Fatal(err)
			}
			if j.RecoveryMode != "cleanup" {
				t.Fatal("not immediate cleanup")
			}
			r, _ := s.Refund(ctx, u.ID, j.ID)
			if r.HTTPStatus != 401 || r.ErrorCode != "password_verification_failed" || r.FeeStatus != "frozen" {
				t.Fatal(r)
			}
			if err = s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: "login"}); !errors.Is(err, store.ErrState) {
				t.Fatal("allowed relogin", err)
			}
			if err = s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: "merge_v2_submitting", TeamID: j.Mother.TeamID}); !errors.Is(err, store.ErrState) {
				t.Fatal("allowed submit", err)
			}
			checkpoint(t, s, j, "cleanup_started")
			if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "mother_network", HTTPStatus: 504, Retryable: true}); err != nil {
				t.Fatal(err)
			}
			if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
				t.Fatal("mother reused or ignored interval", err)
			}
			r, _ = s.Refund(ctx, u.ID, j.ID)
			if r.HTTPStatus != 401 || r.FeeStatus != "frozen" {
				t.Fatal("cause lost", r)
			}
			view, err := s.AdminRefundRecovery(ctx, j.ID)
			if err != nil || !view.Pending || !strings.Contains(view.Reason, "401") {
				t.Fatal(view, err)
			}
			recoveryDue(t, s, j.ID)
			// Child proxy is unnecessary for terminal cleanup.
			if _, err = s.Pool.Exec(ctx, "UPDATE refund_proxies SET enabled=false WHERE id=$1", cfg.ChildProxyID); err != nil {
				t.Fatal(err)
			}
			next, err := s.ClaimRefund(ctx)
			if err != nil || next == nil || next.RecoveryMode != "cleanup" || next.ChildProxy != "" {
				t.Fatal("cleanup not recovered", next, err)
			}
			checkpoint(t, s, next, "cleanup_started")
			checkpoint(t, s, next, "cleanup_completed")
			r, _ = s.Refund(ctx, u.ID, j.ID)
			if r.Status != "failed" || r.FeeStatus != "released" || r.HTTPStatus != 401 || !strings.Contains(r.Message, "密码验证失败") {
				t.Fatal(r)
			}
			timeline, err := s.AdminRefundTimeline(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range timeline.Items {
				if item.Stage == "登" && (item.Status != "未通过" || item.FinishedAt == nil) {
					t.Fatal(item)
				}
				if (item.Stage == "团" || item.Stage == "停") && item.Status != "未执行" {
					t.Fatal(item)
				}
				if item.Stage == "结" && (item.Status != "已结束" || item.FinishedAt == nil) {
					t.Fatal(item)
				}
			}
			if err = s.RefundCheckpoint(ctx, next, store.Checkpoint{Method: "cleanup_completed"}); !errors.Is(err, store.ErrState) {
				t.Fatal("duplicate completion", err)
			}
			if err = s.FinishRefundAttempt(ctx, j, store.RefundDiagnostic{Code: "login_failed"}); err != nil {
				t.Fatal(err)
			}
			var releases, settles int
			s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE kind='release'),count(*) FILTER(WHERE kind='settlement') FROM ledger WHERE refund_id=$1", j.ID).Scan(&releases, &settles)
			if releases != 1 || settles != 0 {
				t.Fatal(releases, settles)
			}
			events, _ := s.Events(ctx, u.ID, j.ID)
			raw, _ := json.Marshal(events)
			if !strings.Contains(string(raw), "401") || strings.Contains(string(raw), "invites") || strings.Contains(string(raw), "cleanup") {
				t.Fatal(string(raw))
			}
			if _, err = s.Refund(ctx, a.ID, j.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("cross-user read", err)
			}
			rows, _, err := s.AdminRefunds(ctx, "", "failed", 1, 10)
			if err != nil || len(rows) != 1 || rows[0].HTTPStatus != 401 {
				t.Fatal(rows, err)
			}
			monitor, err := s.AdminRefundMonitor(ctx, store.RefundMonitorFilter{Status: "failed", Page: 1, Size: 10})
			if err != nil || len(monitor.Items) != 1 || monitor.Items[0].HTTPStatus != 401 {
				t.Fatal(monitor, err)
			}
		})
	}
}

func TestPassword401CheckpointGuardsAndInterruptedCleanup(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	enableRecovery(t, s, a)
	ctx := context.Background()
	cfg, _ := s.RefundSettings(ctx)
	cfg.Recovery.Enabled = false
	s.SaveRefundSettings(ctx, a.ID, cfg)
	submitRequests(t, s, u, 1)
	j, _ := s.ClaimRefund(ctx)
	if err := s.RefundCheckpoint(ctx, j, passwordCheckpoint()); !errors.Is(err, store.ErrState) {
		t.Fatal("wrong step", err)
	}
	checkpoint(t, s, j, "inviting")
	checkpoint(t, s, j, "invited")
	checkpoint(t, s, j, "login")
	for _, mutate := range []func(*store.Checkpoint){
		func(c *store.Checkpoint) { c.Diagnostic.HTTPStatus = 403 },
		func(c *store.Checkpoint) { c.Diagnostic.Evidence.Side = "mother" },
		func(c *store.Checkpoint) { c.Diagnostic.Evidence.Stage = "mfa_verify" },
	} {
		c := passwordCheckpoint()
		mutate(&c)
		if err := s.RefundCheckpoint(ctx, j, c); !errors.Is(err, store.ErrState) {
			t.Fatal("misclassified 401", err)
		}
	}
	if err := s.RefundCheckpoint(ctx, j, passwordCheckpoint()); err != nil {
		t.Fatal(err)
	}
	s.Pool.Exec(ctx, "UPDATE refund_execution SET lease_until=now()-interval '1 second' WHERE refund_id=$1", j.ID)
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("expected interruption scheduling", err)
	}
	recoveryDue(t, s, j.ID)
	next, err := s.ClaimRefund(ctx)
	if err != nil || next == nil || next.RecoveryMode != "cleanup" {
		t.Fatal(next, err)
	}
	for n := 0; n < 3; n++ {
		if err = s.FinishRefundAttempt(ctx, next, store.RefundDiagnostic{Code: "cleanup_invite_pending"}); err != nil {
			t.Fatal(err)
		}
		r, _ := s.Refund(ctx, u.ID, j.ID)
		if n < 2 {
			recoveryDue(t, s, j.ID)
			next, err = s.ClaimRefund(ctx)
			if err != nil || next == nil {
				t.Fatal(err)
			}
		} else if r.Status != "attention" || r.HTTPStatus != 401 || r.FeeStatus != "frozen" {
			t.Fatal(r)
		}
	}
	if err = s.ResumeRefund(ctx, a.ID, j.ID, "resume", "修复母号后继续清理"); err != nil {
		t.Fatal(err)
	}
	next, err = s.ClaimRefund(ctx)
	if err != nil || next == nil || next.RecoveryMode != "cleanup" {
		t.Fatal("admin resume relogged", next, err)
	}
	checkpoint(t, s, next, "cleanup_started")
	checkpoint(t, s, next, "cleanup_completed")
}

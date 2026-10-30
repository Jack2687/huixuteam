package refundworker_test

import (
	"context"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredStatusRealTransportThenFreeWithoutSecondSubmission(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("REFUND_PYTHON")
	if python == "" {
		python = filepath.Join(root, ".local", "python", "Scripts", "python.exe")
	}
	if _, err = os.Stat(python); err != nil {
		t.Skip("protocol Python environment required")
	}
	s, a, u, _ := executionFixture(t, 1, 1)
	ctx := context.Background()
	cfg, _ := s.RefundSettings(ctx)
	cfg.Recovery.VerificationHTTPStatuses = []int{404, 409}
	cfg.Recovery.PlanFallback = &store.PlanFallbackSettings{Enabled: true, MaxCycles: 2, MaxAttempts: 3, RetrySeconds: 30}
	if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "configured-response", []refundprovider.Account{{Email: "configured@example.test", Password: "fixture-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	if err != nil {
		t.Fatal(err)
	}
	worker := refundworker.Worker{DB: s, Runner: refundworker.ProtocolRunner{Python: python, Script: filepath.Join(root, "internal", "refundworker", "testdata", "verification_runner.py")}}
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	worker.Execute(ctx, j)
	r, _ := s.Refund(ctx, u.ID, ids[0])
	if r.Status != "processing" || r.FeeStatus != "frozen" {
		t.Fatal("premature failure", r)
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET next_attempt_at=now()-interval '1 second' WHERE refund_id=$1", j.ID); err != nil {
		t.Fatal(err)
	}
	j, err = s.ClaimRefund(ctx)
	if err != nil || j == nil || !j.VerificationOnly || j.RecoveryMode != "fallback" {
		t.Fatal("missing durable policy", j, err)
	}
	worker.Execute(ctx, j)
	r, _ = s.Refund(ctx, u.ID, ids[0])
	if r.Status != "succeeded" || r.FeeStatus != "settled" {
		t.Fatal("verification did not complete", r)
	}
}

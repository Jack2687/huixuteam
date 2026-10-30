package refundworker_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/refundworker"
)

func TestLoginResponsesRecoverThroughActualWorkerAndTransport(t *testing.T) {
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
	for _, scenario := range []string{"incomplete", "verification"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, u, _ := executionFixture(t, 1, 1)
			ctx := context.Background()
			ids, _, err := s.CreateRefunds(ctx, u.ID, "login-step-"+scenario, []refundprovider.Account{{Email: scenario + "@example.test", Password: "fixture-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
			if err != nil {
				t.Fatal(err)
			}
			w := refundworker.Worker{DB: s, Runner: refundworker.ProtocolRunner{Python: python, Script: filepath.Join(root, "internal", "refundworker", "testdata", "login_steps_runner.py")}}
			j, err := s.ClaimRefund(ctx)
			if err != nil || j == nil {
				t.Fatal(j, err)
			}
			w.Execute(ctx, j)
			r, _ := s.Refund(ctx, u.ID, ids[0])
			if r.Status != "processing" || r.FeeStatus != "frozen" {
				t.Fatal("premature attention or release", r)
			}
			if early, err := s.ClaimRefund(ctx); err != nil || early != nil {
				t.Fatal("interval ignored", early, err)
			}
			if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET next_attempt_at=now()-interval '1 second' WHERE refund_id=$1", j.ID); err != nil {
				t.Fatal(err)
			}
			next, err := s.ClaimRefund(ctx)
			if err != nil || next == nil || next.ID != j.ID || next.Mother.ID != j.Mother.ID {
				t.Fatal(next, err)
			}
			wantMode, wantStatus, wantFee := "retry", "succeeded", "settled"
			if scenario == "verification" {
				wantMode, wantStatus, wantFee = "cleanup", "failed", "released"
			}
			if next.RecoveryMode != wantMode {
				t.Fatal(next.RecoveryMode)
			}
			if wantMode == "retry" && next.ChildProxy != j.ChildProxy {
				t.Fatal("retry changed child proxy")
			}
			w.Execute(ctx, next)
			r, _ = s.Refund(ctx, u.ID, ids[0])
			if r.Status != wantStatus || r.FeeStatus != wantFee {
				t.Fatal(r)
			}
			var entries int
			if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM ledger WHERE refund_id=$1", j.ID).Scan(&entries); err != nil || entries != 2 {
				t.Fatal("duplicate ledger entries", entries, err)
			}
		})
	}
}

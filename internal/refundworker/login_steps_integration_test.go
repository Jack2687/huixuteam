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
	for _, scenario := range []string{"incomplete", "verification", "verification_persistent"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, u, _ := executionFixture(t, 1, 1)
			ctx := context.Background()
			cfg, err := s.RefundSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Recovery.MaxRetries = 2
			if err := s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
				t.Fatal(err)
			}
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
			if next.RecoveryMode != wantMode {
				t.Fatal(next.RecoveryMode)
			}
			if wantMode == "retry" && next.ChildProxy != j.ChildProxy {
				t.Fatal("retry changed child proxy")
			}
			w.Execute(ctx, next)
			if scenario == "verification_persistent" {
				// The first configured retry still required verification. Run the
				// second retry, then cleanup; each must wait for its own due time.
				for _, mode := range []string{"retry", "cleanup"} {
					r, _ = s.Refund(ctx, u.ID, ids[0])
					if r.Status != "processing" || r.FeeStatus != "frozen" {
						t.Fatal("early release", r)
					}
					if early, err := s.ClaimRefund(ctx); err != nil || early != nil {
						t.Fatal("interval ignored", early, err)
					}
					if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET next_attempt_at=now()-interval '1 second' WHERE refund_id=$1", j.ID); err != nil {
						t.Fatal(err)
					}
					next, err = s.ClaimRefund(ctx)
					if err != nil || next == nil || next.RecoveryMode != mode {
						t.Fatal(next, err)
					}
					w.Execute(ctx, next)
				}
				wantStatus, wantFee = "failed", "released"
			}
			r, _ = s.Refund(ctx, u.ID, ids[0])
			if r.Status != wantStatus || r.FeeStatus != wantFee {
				t.Fatal(r)
			}
			if scenario == "verification_persistent" && r.Message != "登录需要额外验证码，服务费已释放，可再次尝试。" {
				t.Fatal(r.Message)
			}
			var entries int
			if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM ledger WHERE refund_id=$1", j.ID).Scan(&entries); err != nil || entries != 2 {
				t.Fatal("duplicate ledger entries", entries, err)
			}
		})
	}
}

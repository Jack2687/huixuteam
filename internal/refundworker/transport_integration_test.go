package refundworker_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func executionFixture(t *testing.T, parallel, mothers int) (*store.Store, store.User, store.User, store.RefundSettings) {
	t.Helper()
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "workflow@test.invalid", "member")
	ctx := context.Background()
	if err := s.Credit(ctx, u.Email, 90000, "transport fixture"); err != nil {
		t.Fatal(err)
	}
	child, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "Child", URL: "http://child:fixture@child.invalid:8080", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mother, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "Mother", URL: "http://mother:fixture@mother.invalid:8081", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < mothers; i++ {
		_, err = s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: fmt.Sprintf("Fixture %d", i), Email: fmt.Sprintf("mother%d@test.invalid", i), TeamID: fmt.Sprintf("team-fixture-%d", i), AccessToken: "fixture-mother-token", ProxyID: mother, SeatType: "default", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := store.RefundSettings{ChildProxyID: child, Enabled: true, MaxParallel: parallel, LoginAttempts: 2, RetrySeconds: 5}
	if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	return s, a, u, cfg
}

func TestDatabaseWorkerOriginalPythonAndActualProxyTransport(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("REFUND_PYTHON")
	if python == "" {
		python = filepath.Join(root, ".local", "python", "Scripts", "python.exe")
	}
	if _, err = os.Stat(python); err != nil {
		t.Skip("configured protocol Python environment required")
	}
	for _, scenario := range []string{"success", "cleanup", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, u, _ := executionFixture(t, 1, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ids, _, err := s.CreateRefunds(ctx, u.ID, "actual-transport-"+scenario, []refundprovider.Account{{Email: scenario + "@example.test", Password: "fixture-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
			if err != nil {
				t.Fatal(err)
			}
			worker := refundworker.Worker{DB: s, Runner: refundworker.ProtocolRunner{Python: python, Script: filepath.Join(root, "internal", "refundworker", "testdata", "transport_runner.py")}}
			job, err := s.ClaimRefund(ctx)
			if err != nil || job == nil {
				t.Fatal("missing job", err)
			}
			worker.Execute(ctx, job)
			if scenario != "success" {
				r, err := s.Refund(ctx, u.ID, ids[0])
				if err != nil {
					t.Fatal(err)
				}
				if (r.Status != "processing" && r.Status != "verifying") || r.FeeStatus != "frozen" {
					t.Fatalf("failure settled prematurely: %+v", r)
				}
				x, err := s.RefundExecution(ctx, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unknown" {
					if x.Step != "submitting" {
						t.Fatal("lost uncertain checkpoint", x.Step)
					}
					if err := s.ResumeRefund(ctx, a.ID, job.ID, "resume", "fixture review"); err == nil {
						t.Fatal("unknown submission resumed without verification")
					}
					if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET next_attempt_at=now()-interval '1 second' WHERE refund_id=$1", job.ID); err != nil {
						t.Fatal(err)
					}
					verification, err := s.ClaimRefund(ctx)
					if err != nil || verification == nil || verification.RecoveryMode != "verify" {
						t.Fatal("missing verification", err)
					}
					worker.Execute(ctx, verification)
					view, err := s.AdminRefundRecovery(ctx, job.ID)
					if err != nil || view.Checks != 1 || view.NextAt == nil {
						t.Fatal("verification not scheduled", view, err)
					}
					r, _ = s.Refund(ctx, u.ID, job.ID)
					if r.FeeStatus != "frozen" || r.Status != "verifying" {
						t.Fatal("verification changed financial outcome", r)
					}
					return
				}
				if x.Step != "removing" {
					t.Fatal("lost cleanup checkpoint", x.Step)
				}
				// The scheduler resumes cleanup without an administrator action.
				if _, err := s.Pool.Exec(ctx, "UPDATE refund_execution SET next_attempt_at=now()-interval '1 second' WHERE refund_id=$1", job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = s.ClaimRefund(ctx)
				if err != nil || job == nil {
					t.Fatal("missing resumed job", err)
				}
				worker.Execute(ctx, job)
			}
			r, err := s.Refund(ctx, u.ID, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "succeeded" || r.FeeStatus != "settled" {
				t.Fatalf("full flow incomplete: %+v", r)
			}
			user, err := s.User(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if user.Balance != 87000 || user.Frozen != 0 {
				t.Fatal("fee not settled exactly once")
			}
			if err := s.RefundCheckpoint(ctx, job, store.Checkpoint{Method: "completed"}); !errors.Is(err, store.ErrState) {
				t.Fatal("released lease accepted another checkpoint", err)
			}
			user, _ = s.User(ctx, u.ID)
			if user.Balance != 87000 {
				t.Fatal("duplicate completion charged twice")
			}
		})
	}
}

func TestTwoWorkerLoopsRespectMotherSerializationCapacityAndPause(t *testing.T) {
	s, a, u, cfg := executionFixture(t, 2, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	accounts := make([]refundprovider.Account, 6)
	for i := range accounts {
		accounts[i] = refundprovider.Account{Email: fmt.Sprintf("queued%d@example.test", i), Password: "fixture-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "worker-concurrency", accounts)
	if err != nil {
		t.Fatal(err)
	}
	type running struct {
		id, mother string
		release    chan struct{}
	}
	started := make(chan running, 10)
	finished := make(chan string, 10)
	var mu sync.Mutex
	active := map[string]bool{}
	peak := 0
	runner := mockRunner(func(ctx context.Context, j *store.RefundJob, cb func(store.Checkpoint) error) error {
		mu.Lock()
		if active[j.Mother.ID] {
			t.Error("same mother executed concurrently")
		}
		active[j.Mother.ID] = true
		if len(active) > peak {
			peak = len(active)
		}
		if len(active) > 2 {
			t.Error("global concurrency limit exceeded")
		}
		mu.Unlock()
		r := running{j.ID, j.Mother.ID, make(chan struct{})}
		started <- r
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.release:
		}
		for _, method := range []string{"inviting", "invited", "login", "merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted", "removing"} {
			if err := cb(store.Checkpoint{Method: method, TeamID: j.Mother.TeamID, UserID: "fixture-child"}); err != nil {
				return err
			}
		}
		// No more upstream activity after this point; completion releases the mother.
		mu.Lock()
		delete(active, j.Mother.ID)
		mu.Unlock()
		err := cb(store.Checkpoint{Method: "completed"})
		finished <- j.ID
		return err
	})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w := refundworker.Worker{DB: s, Runner: runner}; w.Run(ctx) }()
	}
	defer func() { cancel(); wg.Wait() }()
	next := func() running {
		t.Helper()
		select {
		case r := <-started:
			return r
		case <-ctx.Done():
			t.Fatal("worker did not start queued job")
			return running{}
		}
	}
	done := func() {
		t.Helper()
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("worker did not finish")
		}
	}
	first, second := next(), next()
	if first.mother == second.mother {
		t.Fatal("same mother claimed twice")
	}
	if j, err := s.ClaimRefund(ctx); err != nil || j != nil {
		t.Fatal("third job ignored capacity", err)
	}
	close(first.release)
	done()
	third := next()
	cfg.Enabled = false
	if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	close(second.release)
	close(third.release)
	done()
	done()
	select {
	case <-started:
		t.Fatal("disabled queue started another job")
	case <-time.After(2200 * time.Millisecond):
	}
	for _, id := range ids {
		r, err := s.Refund(ctx, u.ID, id)
		if err != nil || (r.Status != "queued" && r.Status != "succeeded") {
			t.Fatal("pause damaged in-flight job", err)
		}
	}
	cfg.Enabled = true
	if err = s.SaveRefundSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r := next()
		close(r.release)
		done()
	}
	for _, id := range ids {
		r, err := s.Refund(ctx, u.ID, id)
		if err != nil || r.Status != "succeeded" || r.FeeStatus != "settled" {
			t.Fatal("queued job not completed exactly once", err)
		}
	}
	user, _ := s.User(ctx, u.ID)
	if user.Balance != 72000 || user.Frozen != 0 {
		t.Fatal("concurrent settlement incorrect")
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 {
		t.Fatal("multiple mothers did not run concurrently")
	}
}

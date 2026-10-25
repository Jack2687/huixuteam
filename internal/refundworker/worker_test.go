package refundworker_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

type mockRunner func(context.Context, *store.RefundJob, func(store.Checkpoint) error) error

func (f mockRunner) Run(ctx context.Context, j *store.RefundJob, cb func(store.Checkpoint) error) error {
	return f(ctx, j, cb)
}

func TestProtocolProcessCheckpointWireAndRedactedFailure(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python unavailable")
	}
	script := filepath.Join(t.TempDir(), "协议测试.py")
	source := `import json,sys
j=json.loads(sys.stdin.readline())
assert j['mother']['name']=='测试母号'
assert j['account']['password']=='private-password'
assert len(sys.argv)==1
print(json.dumps({'type':'checkpoint','method':'merge_v2_ready','team_id':'team-fixture','user_id':'child-user'}),flush=True)
ack=json.loads(sys.stdin.readline())
print(json.dumps({'type':'result','ok':ack['ok'],'code':'private-password'}),flush=True)
`
	if err = os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	p := refundworker.ProtocolRunner{Python: python, Script: script}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j := &store.RefundJob{Mother: store.RefundMother{Name: "测试母号"}, Account: refundprovider.Account{Password: "private-password"}}
	called := false
	err = p.Run(ctx, j, func(c store.Checkpoint) error {
		called = true
		if c.Method != "merge_v2_ready" || c.TeamID != "team-fixture" || c.UserID != "child-user" {
			t.Errorf("wrong checkpoint: %+v", c)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("protocol pipe failed", err)
	}
	err = p.Run(ctx, j, func(c store.Checkpoint) error { return store.ErrState })
	if err == nil || err.Error() == "private-password" {
		t.Fatal("failed checkpoint or private error not protected", err)
	}
}
func TestWorkerFullFlowThenAcceptedFailureResumesWithoutResubmit(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "worker@test.invalid", "member")
	ctx := context.Background()
	if err := s.Credit(ctx, u.Email, 9000, "fixture"); err != nil {
		t.Fatal(err)
	}
	proxy, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "Fixture", URL: "http://proxy.invalid:8080", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: "Fixture", Email: "mother@test.invalid", TeamID: "team-fixture", AccessToken: "not-real", ProxyID: proxy, SeatType: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRefundSettings(ctx, a.ID, store.RefundSettings{ChildProxyID: proxy, Enabled: true, MaxParallel: 1, LoginAttempts: 1, RetrySeconds: 5}); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "worker-full-flow", []refundprovider.Account{{Email: "customer@test.invalid", Password: "private", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	worker := refundworker.Worker{DB: s, Runner: mockRunner(func(ctx context.Context, j *store.RefundJob, cb func(store.Checkpoint) error) error {
		if j.Step != "assigned" {
			t.Fatal("wrong starting step")
		}
		for _, step := range []string{"inviting", "invited", "login", "merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted"} {
			if err := cb(store.Checkpoint{Method: step, TeamID: j.Mother.TeamID, UserID: "child-user"}); err != nil {
				return err
			}
			if step == "merge_v2_submitting" {
				posts++
			}
		}
		return errors.New("模拟移出团队时连接中断")
	})}
	job, err := s.ClaimRefund(ctx)
	if err != nil {
		t.Fatal(err)
	}
	worker.Execute(ctx, job)
	r, _ := s.Refund(ctx, u.ID, ids[0])
	if r.Status != "attention" || r.FeeStatus != "frozen" {
		t.Fatalf("failure prematurely settled %+v", r)
	}
	if err = s.ResumeRefund(ctx, a.ID, ids[0], "resume", "checked mother connection"); err != nil {
		t.Fatal(err)
	}
	worker.Runner = mockRunner(func(ctx context.Context, j *store.RefundJob, cb func(store.Checkpoint) error) error {
		if j.Step != "accepted" || j.ChildUserID != "child-user" {
			t.Fatal("lost accepted checkpoint")
		}
		if err := cb(store.Checkpoint{Method: "removing"}); err != nil {
			return err
		}
		return cb(store.Checkpoint{Method: "completed"})
	})
	job, err = s.ClaimRefund(ctx)
	if err != nil {
		t.Fatal(err)
	}
	worker.Execute(ctx, job)
	r, _ = s.Refund(ctx, u.ID, ids[0])
	if r.Status != "succeeded" || r.FeeStatus != "settled" || posts != 1 {
		t.Fatalf("wrong completion: %+v posts %d", r, posts)
	}
	user, _ := s.User(ctx, u.ID)
	if user.Balance != 6000 || user.Frozen != 0 {
		t.Fatal("incorrect final fees")
	}
}

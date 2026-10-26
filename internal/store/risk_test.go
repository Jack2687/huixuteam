package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestRiskDurableParallelLimitsExpiryAndModes(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	rule := store.RiskRule{Mode: "enforce", Limit: 10, WindowSeconds: 60, CooldownSeconds: 90}
	identity := store.RiskIdentity{Type: "login", ID: "account@test.invalid", Label: "account@test.invalid"}
	consume := func(db *store.Store, p store.RiskRule) (store.RiskDecision, error) {
		ds, e := db.ConsumeRisk(ctx, []store.RiskConsumption{{Rule: "login", Policy: p, Identity: identity}})
		if len(ds) == 0 {
			return store.RiskDecision{}, e
		}
		return ds[0], e
	}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := consume(s, rule)
			if e != nil {
				t.Error(e)
				return
			}
			if !d.Blocked {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatal("parallel reservation allowed", allowed.Load())
	}
	// A fresh Store and pool using the same schema must retain the restriction.
	reopened, err := store.Open(ctx, s.Pool.Config().ConnString(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Pool.Close()
	d, err := consume(reopened, rule)
	if err != nil || !d.Blocked || d.RetrySeconds < 80 || d.RetrySeconds > 90 {
		t.Fatal("durability", d, err)
	}
	var before, after time.Time
	s.Pool.QueryRow(ctx, "SELECT blocked_until FROM risk_buckets WHERE id=$1", d.BucketID).Scan(&before)
	consume(s, rule)
	s.Pool.QueryRow(ctx, "SELECT blocked_until FROM risk_buckets WHERE id=$1", d.BucketID).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("denied calls extended cooldown")
	}
	_, err = s.Pool.Exec(ctx, "UPDATE risk_buckets SET blocked_until=now()-interval '1 second' WHERE id=$1", d.BucketID)
	if err != nil {
		t.Fatal(err)
	}
	d, err = consume(s, rule)
	if err != nil || d.Blocked {
		t.Fatal("expired cooldown", d, err)
	}
	rule.Limit = 1
	rule.Mode = "observe"
	consume(s, rule)
	d, err = consume(s, rule)
	if err != nil || !d.Exceeded || d.Blocked {
		t.Fatal("observe mode blocked", d, err)
	}
	rule.Mode = "off"
	d, err = consume(s, rule)
	if err != nil || d.Blocked || d.Exceeded {
		t.Fatal("off mode", d, err)
	}
	rule.Mode = "enforce"
	consume(s, rule)
	_, err = s.Pool.Exec(ctx, "UPDATE risk_buckets SET window_until=now()-interval '1 second' WHERE rule='login'")
	if err != nil {
		t.Fatal(err)
	}
	d, err = consume(s, rule)
	if err != nil || d.Blocked {
		t.Fatal("window expiry", d, err)
	}
}

func TestRiskConfigurationRevisionIsolationAndControls(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "rules@test.invalid", "member")
	ctx := context.Background()
	cfg, err := s.RiskSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules["login"].Limit != 10 || cfg.Rules["query"].Mode != "observe" || cfg.RetentionDays != 30 || cfg.QueueSeconds != 600 {
		t.Fatal("unsafe default", cfg)
	}
	if err = s.SaveRiskSettings(ctx, u.ID, cfg); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member changed rules", err)
	}
	invalid := cfg
	invalid.TrustedProxies = []string{"0.0.0.0/0"}
	if err = s.SaveRiskSettings(ctx, a.ID, invalid); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("all addresses trusted", err)
	}
	for _, name := range []string{"login", "query"} {
		_, err = s.ConsumeRisk(ctx, []store.RiskConsumption{{Rule: name, Policy: cfg.Rules[name], Identity: store.RiskIdentity{Type: "user", ID: u.ID, UserID: u.ID}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	r := cfg.Rules["query"]
	r.Limit = 400
	cfg.Rules["query"] = r
	if err = s.SaveRiskSettings(ctx, a.ID, cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRiskSettings(ctx, a.ID, cfg); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("stale config accepted", err)
	}
	var login, query int
	s.Pool.QueryRow(ctx, "SELECT count(*) FILTER(WHERE rule='login'),count(*) FILTER(WHERE rule='query') FROM risk_buckets").Scan(&login, &query)
	if login != 1 || query != 0 {
		t.Fatal("reset unrelated rule", login, query)
	}
	control := store.RiskUserControl{UserID: u.ID, Limits: map[string]int{"query": 900}, Paused: true}
	if err = s.SaveRiskUserControl(ctx, a.ID, control); err != nil {
		t.Fatal(err)
	}
	got, err := s.RiskUserControl(ctx, u.ID)
	if err != nil || got.Limits["query"] != 900 || !got.Paused {
		t.Fatal(got, err)
	}
	control.Limits["login"] = 999
	if err = s.SaveRiskUserControl(ctx, a.ID, control); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("login override allowed", err)
	}
	if _, err = s.RiskUserControl(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing client", err)
	}
}

func TestRiskPauseOnlyNewApplicationsAndFinance(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "paused@test.invalid", "member")
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.Credit(ctx, u.Email, 30000, "risk-test"))
	accounts := []refundprovider.Account{account("running@test.invalid"), account("cancel@test.invalid")}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "risk-existing", accounts)
	must(err)
	must(s.SaveRiskUserControl(ctx, a.ID, store.RiskUserControl{UserID: u.ID, Paused: true, Limits: map[string]int{}}))
	_, replay, err := s.CreateRefunds(ctx, u.ID, "risk-existing", accounts)
	must(err)
	if !replay {
		t.Fatal("paused idempotent replay failed")
	}
	_, _, err = s.CreateRefunds(ctx, u.ID, "risk-new", []refundprovider.Account{account("new@test.invalid")})
	if !errors.Is(err, store.ErrSubmissionPaused) {
		t.Fatal("pause didn't protect new submit", err)
	}
	must(s.Transition(ctx, u.ID, ids[1], "cancelled", ""))
	must(s.Transition(ctx, u.ID, ids[0], "processing", "开始处理"))
	must(s.Transition(ctx, u.ID, ids[0], "succeeded", "完成"))
	must(s.ScanRisk(ctx))
	m, err := s.RiskMonitor(ctx, store.RiskMonitorFilter{Category: "finance"})
	must(err)
	for _, e := range m.Items {
		if e.Kind == "frozen_mismatch" || e.Kind == "settlement_mismatch" {
			t.Fatal("valid finances falsely flagged", e)
		}
	}
	_, err = s.Pool.Exec(ctx, "UPDATE risk_user_controls SET paused_until=now()-interval '1 second' WHERE user_id=$1", u.ID)
	must(err)
	_, _, err = s.CreateRefunds(ctx, u.ID, "risk-new", []refundprovider.Account{account("new@test.invalid")})
	must(err)
}

func TestRiskScannerRecurrenceRetentionAndSafeTelemetry(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "scanner@test.invalid", "member")
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	cfg, err := s.RiskSettings(ctx)
	must(err)
	cfg.QueueCount = 1
	cfg.DuplicateCount = 2
	cfg.RetentionDays = 1
	must(s.SaveRiskSettings(ctx, a.ID, cfg))
	must(s.Credit(ctx, u.Email, 6000, "test"))
	ids, _, err := s.CreateRefunds(ctx, u.ID, "scan-queue", []refundprovider.Account{account("queue@test.invalid")})
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at=now()-interval '20 minutes',message='private-canary' WHERE id=$1", ids[0])
	must(err)
	obs := store.RiskObservation{UserID: u.ID, Account: u.Email, Operation: "submit", Channel: "api", Status: 409, DurationMS: 3, SourceIP: "198.51.100.2"}
	must(s.RecordRiskRequest(ctx, obs, "duplicate"))
	obs.DurationMS = 4
	must(s.RecordRiskRequest(ctx, obs, "duplicate"))
	run := func() {
		_, e := s.Pool.Exec(ctx, "UPDATE risk_scan_state SET last_scan='epoch'")
		must(e)
		must(s.ScanRisk(ctx))
	}
	run()
	m, err := s.RiskMonitor(ctx, store.RiskMonitorFilter{})
	must(err)
	if m.Summary.Requests != 2 || m.Summary.Errors != 2 || len(m.Traffic) != 1 || m.Traffic[0].AverageMS != 4 {
		t.Fatal("traffic aggregation", m.Summary, m.Traffic)
	}
	var queueEvent int64
	found := map[string]bool{}
	for _, e := range m.Items {
		found[e.Kind] = true
		if e.Kind == "queue_many" {
			queueEvent = e.ID
		}
	}
	if !found["queue_long"] || !found["duplicate_many"] || queueEvent == 0 {
		t.Fatal("missing alerts", found)
	}
	must(s.ResolveRiskEvent(ctx, a.ID, queueEvent))
	run()
	var resolved bool
	must(s.Pool.QueryRow(ctx, "SELECT resolved_at IS NOT NULL FROM risk_events WHERE id=$1", queueEvent).Scan(&resolved))
	if !resolved {
		t.Fatal("ongoing alert reopened")
	}
	must(s.Transition(ctx, u.ID, ids[0], "cancelled", ""))
	run()
	_, _, err = s.CreateRefunds(ctx, u.ID, "scan-again", []refundprovider.Account{account("again@test.invalid")})
	must(err)
	run()
	var n int
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM risk_events WHERE kind='queue_many' AND resolved_at IS NULL").Scan(&n))
	if n != 1 {
		t.Fatal("recurrence didn't alert", n)
	}
	_, err = s.Pool.Exec(ctx, "UPDATE users SET frozen=frozen+1 WHERE id=$1", u.ID)
	must(err)
	run()
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM risk_events WHERE kind='frozen_mismatch'").Scan(&n))
	if n != 1 {
		t.Fatal("imbalance not found")
	}
	_, err = s.Pool.Exec(ctx, "UPDATE risk_traffic SET minute=now()-interval '3 days'")
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE risk_events SET last_seen=now()-interval '3 days' WHERE kind='duplicate'")
	must(err)
	run()
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM risk_traffic").Scan(&n))
	if n != 0 {
		t.Fatal("retention didn't clear stats")
	}
	var applicants int
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM refunds").Scan(&applicants))
	if applicants != 2 {
		t.Fatal("retention touched business data")
	}
}

func TestRiskBehaviorThresholdsAndDisabledRecording(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "behavior@test.invalid", "member")
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	cfg, err := s.RiskSettings(ctx)
	must(err)
	cfg.LoginSourceAccounts = 2
	cfg.CancelCount = 1
	cfg.FailureMinCount = 2
	cfg.FailurePercent = 50
	cfg.LargeAdjustment = 10000
	must(s.SaveRiskSettings(ctx, a.ID, cfg))
	must(s.AdminCredit(ctx, a.ID, u.ID, "behavior-funding", 50000, "fixture"))
	for _, verified := range []bool{true, false} {
		ip := "198.51.100.10"
		if !verified {
			ip = "198.51.100.11"
		}
		for _, email := range []string{"one@test.invalid", "two@test.invalid"} {
			must(s.RecordRiskRequest(ctx, store.RiskObservation{Account: email, SourceIP: ip, SourceVerified: verified, Operation: "login", Status: 401}, "login_failed"))
		}
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "behavior", []refundprovider.Account{account("failure@test.invalid"), account("success@test.invalid"), account("cancelled@test.invalid"), account("slow@test.invalid")})
	must(err)
	for _, id := range []string{ids[0], ids[1], ids[3]} {
		must(s.Transition(ctx, u.ID, id, "processing", "开始处理"))
	}
	must(s.Transition(ctx, u.ID, ids[0], "failed", ""))
	must(s.Transition(ctx, u.ID, ids[1], "succeeded", ""))
	must(s.Transition(ctx, u.ID, ids[2], "cancelled", ""))
	_, err = s.Pool.Exec(ctx, "UPDATE refund_events SET created_at=now()-interval '20 minutes' WHERE refund_id=$1 AND message='开始处理'", ids[3])
	must(err)
	must(s.ScanRisk(ctx))
	m, err := s.RiskMonitor(ctx, store.RiskMonitorFilter{})
	must(err)
	found := map[string]int{}
	for _, e := range m.Items {
		found[e.Kind]++
		if e.Kind == "login_source" && e.SourceIP != "198.51.100.10" {
			t.Fatal("unverified proxy shared address flagged")
		}
	}
	for _, kind := range []string{"login_source", "failure_rate", "cancel_many", "processing_long", "large_adjustment"} {
		if found[kind] != 1 {
			t.Fatal("missing threshold", kind, found)
		}
	}
	if found["frozen_mismatch"] > 0 || found["settlement_mismatch"] > 0 {
		t.Fatal("legitimate ledger flagged", found)
	}
	cfg, err = s.RiskSettings(ctx)
	must(err)
	cfg.Enabled = false
	must(s.SaveRiskSettings(ctx, a.ID, cfg))
	_, err = s.Pool.Exec(ctx, "DELETE FROM risk_events")
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE risk_scan_state SET last_scan='epoch'")
	must(err)
	must(s.ScanRisk(ctx))
	var count int
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM risk_events").Scan(&count))
	if count != 0 {
		t.Fatal("disabled scan recorded events")
	}
}

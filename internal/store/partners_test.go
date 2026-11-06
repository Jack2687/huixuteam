package store_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"testing"
)

func TestPartnerRewardsStagesSnapshotsAndIsolation(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	owner := testutil.Member(t, s, a, "partner@test.invalid", "member")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := s.Pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	create := func(uid string) string {
		id := store.ID()
		exec("INSERT INTO refunds(id,user_id,email,credentials,fee,status,fee_status) VALUES($1,$2,'test@example.test',''::bytea,10000,'succeeded','settled')", id, uid)
		return id
	}
	charge := func(uid, id string) {
		exec("INSERT INTO ledger(user_id,refund_id,kind,amount,balance_after) VALUES($1,$2,'settlement',-10000,0)", uid, id)
	}
	for i, v := range []struct {
		age    float64
		reward int64
	}{{0, 1000}, {29.99, 1000}, {30, 500}, {59.99, 500}, {60, 200}, {89.99, 200}, {90, 0}, {100, 0}} {
		guest := testutil.Member(t, s, a, fmt.Sprintf("stage%d@test.invalid", i), "member")
		exec("UPDATE users SET invited_by=$2,created_at=now()-$3*interval '1 day' WHERE id=$1", guest.ID, owner.ID, v.age)
		id := create(guest.ID)
		charge(guest.ID, id)
		var earned int64
		if e := s.Pool.QueryRow(ctx, "SELECT COALESCE(sum(amount),0) FROM partner_rewards WHERE order_id=$1", id).Scan(&earned); e != nil {
			t.Fatal(e)
		}
		if earned != v.reward {
			t.Fatalf("age %v earned %d want %d", v.age, earned, v.reward)
		}
		if _, e := s.Pool.Exec(ctx, "INSERT INTO ledger(user_id,refund_id,kind,amount,balance_after) VALUES($1,$2,'settlement',-10000,0)", guest.ID, id); e == nil {
			t.Fatal("duplicate settlement accepted")
		}
	}
	guest := testutil.Member(t, s, a, "snapshot@test.invalid", "member")
	exec("UPDATE users SET invited_by=$2 WHERE id=$1", guest.ID, owner.ID)
	first := create(guest.ID)
	second := create(guest.ID)
	rules := store.PartnerRules{Enabled: false, Stages: []store.PartnerStage{{UntilDays: 30, RatePercent: 10}, {UntilDays: 60, RatePercent: 5}, {UntilDays: 90, RatePercent: 2}}}
	if e := s.SavePartnerRules(ctx, owner.ID, rules); !errors.Is(e, store.ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.SavePartnerRules(ctx, a.ID, rules); e != nil {
		t.Fatal(e)
	}
	charge(guest.ID, first)
	charge(guest.ID, second)
	third := create(guest.ID)
	charge(guest.ID, third)
	var count int
	if e := s.Pool.QueryRow(ctx, "SELECT count(*) FROM partner_rewards WHERE invitee_id=$1", guest.ID).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
	view, e := s.Partners(ctx, guest.ID, "", 1, 20)
	if e != nil || view["total"] != int64(0) {
		t.Fatal(view, e)
	}
	view, e = s.Partners(ctx, owner.ID, "", 1, 20)
	if e != nil || view["total"] != int64(9) {
		t.Fatal(view, e)
	}
	var balance, sum int64
	s.Pool.QueryRow(ctx, "SELECT balance FROM users WHERE id=$1", owner.ID).Scan(&balance)
	s.Pool.QueryRow(ctx, "SELECT sum(amount) FROM ledger WHERE user_id=$1 AND kind='invite_reward'", owner.ID).Scan(&sum)
	if balance != sum || sum != 5400 {
		t.Fatal(balance, sum)
	}
	rules.Stages[1].RatePercent = 11
	if e = s.SavePartnerRules(ctx, a.ID, rules); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("accepted increasing rates", e)
	}
}
func TestPartnerRegistrationBindingAndCreatedAt(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	owner := testutil.Member(t, s, a, "inviter@test.invalid", "member")
	if e := s.SaveSiteSecurity(ctx, a.ID, store.SiteSecurity{RegistrationEnabled: true, LoginSliderEnabled: true}); e != nil {
		t.Fatal(e)
	}
	var code string
	s.Pool.QueryRow(ctx, "SELECT invite_code FROM users WHERE id=$1", owner.ID).Scan(&code)
	if e := s.Register(ctx, store.RegistrationInput{ServiceEligibilityAccepted: true, Email: "guest@test.invalid", Name: "Guest", Password: "LongTestPassword123!", Invite: code}); e != nil {
		t.Fatal(e)
	}
	var bound string
	if e := s.Pool.QueryRow(ctx, "SELECT invited_by FROM users WHERE email='guest@test.invalid'").Scan(&bound); e != nil || bound != owner.ID {
		t.Fatal(bound, e)
	}
	if e := s.Register(ctx, store.RegistrationInput{ServiceEligibilityAccepted: true, Email: "invalid@test.invalid", Name: "Guest", Password: "LongTestPassword123!", Invite: "invalid"}); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	var id string
	if e := s.Pool.QueryRow(ctx, "SELECT id FROM users WHERE email='invalid@test.invalid'").Scan(&id); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal(e)
	}
	users, _, e := s.AdminUsers(ctx, "guest@test.invalid", 1, 20)
	if e != nil || len(users) != 1 || users[0].CreatedAt.IsZero() {
		t.Fatal(users, e)
	}
}
func TestLoginSliderBindingReplayExpiryAndDisabled(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	cfg, e := s.SiteSettings(ctx)
	if e != nil || !cfg.Security.LoginSliderEnabled {
		t.Fatal("default must enable slider", e)
	}
	if e = s.ConsumeLoginSlider(ctx, "a", ""); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	challenge := func() (string, float64) {
		t.Helper()
		v, e := s.LoginSlider(ctx, "a")
		if e != nil {
			t.Fatal(e)
		}
		var x int
		e = s.Pool.QueryRow(ctx, "UPDATE auth_slider_challenges SET created_at=now()-interval '1 second' WHERE id=$1 RETURNING target", v["id"]).Scan(&x)
		if e != nil {
			t.Fatal(e)
		}
		return v["id"].(string), float64(x)
	}
	id, x := challenge()
	if _, e = s.VerifyLoginSlider(ctx, "b", id, x); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	proof, e := s.VerifyLoginSlider(ctx, "a", id, x)
	if e != nil {
		t.Fatal(e)
	}
	token := proof["proof"].(string)
	if e = s.ConsumeLoginSlider(ctx, "b", token); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	if e = s.ConsumeLoginSlider(ctx, "a", token); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeLoginSlider(ctx, "a", token); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	id, x = challenge()
	if _, e = s.VerifyLoginSlider(ctx, "a", id, 0); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	if _, e = s.VerifyLoginSlider(ctx, "a", id, x); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	id, x = challenge()
	s.Pool.Exec(ctx, "UPDATE auth_slider_challenges SET expires_at=now()-interval '1 second'")
	if _, e = s.VerifyLoginSlider(ctx, "a", id, x); !errors.Is(e, store.ErrInvalid) {
		t.Fatal(e)
	}
	cfg.Security.LoginSliderEnabled = false
	if e = s.SaveSiteSecurity(ctx, a.ID, cfg.Security); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeLoginSlider(ctx, "", ""); e != nil {
		t.Fatal(e)
	}
}

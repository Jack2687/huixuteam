package store_test

import (
	"context"
	"errors"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"testing"
)

func TestRegistrationServicePriceConfiguration(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	old := testutil.Member(t, s, a, "existing@test.invalid", "member")
	cfg, err := s.SiteService(ctx)
	if err != nil || cfg.DefaultServiceFee != 10000 {
		t.Fatal("wrong registration default", cfg, err)
	}
	if err = s.SaveSiteService(ctx, old.ID, store.SiteServiceSettings{DefaultServiceFee: 1}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member changed price", err)
	}
	for _, fee := range []int64{-1, 10000001} {
		if err = s.SaveSiteService(ctx, a.ID, store.SiteServiceSettings{DefaultServiceFee: fee}); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("accepted invalid price", fee, err)
		}
	}
	if err = s.SaveSiteSecurity(ctx, a.ID, store.SiteSecurity{RegistrationEnabled: true}); err != nil {
		t.Fatal(err)
	}
	register := func(email string, want int64) {
		t.Helper()
		in := store.RegistrationInput{ServiceEligibilityAccepted: true, Email: email, Name: "QA", Password: "TestPassword-12345"}
		in.ServiceEligibilityAccepted = false
		if err := s.Register(ctx, in); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("registration without eligibility confirmation accepted", err)
		}
		in.ServiceEligibilityAccepted = true
		if err := s.Register(ctx, in); err != nil {
			t.Fatal(err)
		}
		u, err := s.Login(ctx, email, in.Password)
		if err != nil || u.ServiceFee != want || u.Balance != 0 || u.Frozen != 0 || u.Role != "member" {
			t.Fatal("registration price/role/balance", u, err)
		}
	}
	register("default@test.invalid", 10000)
	if err = s.SaveSiteService(ctx, a.ID, store.SiteServiceSettings{DefaultServiceFee: 12345}); err != nil {
		t.Fatal(err)
	}
	register("custom@test.invalid", 12345)
	if err = s.SaveSiteService(ctx, a.ID, store.SiteServiceSettings{DefaultServiceFee: 0}); err != nil {
		t.Fatal(err)
	}
	register("zero@test.invalid", 0)
	old, err = s.User(ctx, old.ID)
	if err != nil || old.ServiceFee != 3000 {
		t.Fatal("existing price overwritten", old.ServiceFee, err)
	}
	u, err := s.Login(ctx, "default@test.invalid", "TestPassword-12345")
	if err != nil || u.ServiceFee != 10000 {
		t.Fatal("registered price changed", err)
	}
}

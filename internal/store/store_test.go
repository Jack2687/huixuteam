package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func account(email string) refundprovider.Account {
	return refundprovider.Account{Email: email, Password: "not-a-real-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}
}
func TestRefundTransactionsEncryptionAndSettlement(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "one@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "initial-credit", 15000, "test funding"); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminCredit(ctx, a.ID, u.ID, "initial-credit", 15000, "test funding"); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminCredit(ctx, a.ID, u.ID, "initial-credit", 15001, "test funding"); !errors.Is(err, store.ErrConflict) {
		t.Fatal("credit idempotency conflict not rejected", err)
	}
	accounts := []refundprovider.Account{account("refund1@test.invalid"), account("refund2@test.invalid")}
	ids, replay, err := s.CreateRefunds(ctx, u.ID, "request-001", accounts)
	if err != nil || replay || len(ids) != 2 {
		t.Fatal(ids, replay, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, repeated, e := s.CreateRefunds(ctx, u.ID, "request-001", accounts)
			if e != nil || !repeated || len(got) != 2 || got[0] != ids[0] {
				t.Error("concurrent idempotency failed", e)
			}
		}()
	}
	wg.Wait()
	u, _ = s.User(ctx, u.ID)
	if u.Balance != 15000 || u.Frozen != 6000 {
		t.Fatalf("wrong freeze: %+v", u)
	}
	var ciphertext []byte
	if err = s.Pool.QueryRow(ctx, "SELECT credentials FROM refunds WHERE id=$1", ids[0]).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(accounts[0].Password)) || bytes.Contains(ciphertext, []byte(accounts[0].TOTPSecret)) {
		t.Fatal("credentials stored in plaintext")
	}
	plain, err := s.Credentials(ctx, u.ID, ids[0])
	if err != nil || plain.Password != accounts[0].Password {
		t.Fatal("encrypted credentials did not roundtrip", err)
	}
	if _, _, err = s.CreateRefunds(ctx, u.ID, "request-rollback", []refundprovider.Account{account("new@test.invalid"), accounts[0]}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatal("duplicate active account accepted", err)
	}
	_, count, err := s.ListRefunds(ctx, u.ID, "", "", 1, 20)
	if err != nil || count != 2 {
		t.Fatal("partial batch persisted", count, err)
	}
	if _, _, err = s.CreateRefunds(ctx, u.ID, "request-001", []refundprovider.Account{account("different@test.invalid")}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("idempotency content mismatch accepted", err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Transition(ctx, u.ID, ids[0], "cancelled", "test cancelled"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Transition(ctx, u.ID, ids[0], "succeeded", "invalid callback"); !errors.Is(err, store.ErrState) {
		t.Fatal("cancelled request settled", err)
	}
	if err = s.Transition(ctx, u.ID, ids[1], "processing", "test processing"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Transition(ctx, u.ID, ids[1], "succeeded", "test success"); err != nil {
			t.Fatal(err)
		}
	}
	u, _ = s.User(ctx, u.ID)
	if u.Balance != 12000 || u.Frozen != 0 {
		t.Fatal("incorrect final balance", u)
	}
	entries, _, err := s.Billing(ctx, u.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	settles, releases := 0, 0
	for _, e := range entries {
		if e.Kind == "settlement" {
			settles++
		}
		if e.Kind == "release" {
			releases++
		}
	}
	if settles != 1 || releases != 1 {
		t.Fatal("repeated financial entry", settles, releases)
	}
	if _, err = (refundprovider.Unconfigured{}).Submit(ctx, "example", accounts[0]); !errors.Is(err, refundprovider.ErrNotConfigured) {
		t.Fatal("placeholder provider performed work", err)
	}
}
func TestConcurrentRedeemAndLimits(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	users := []store.User{}
	for i := 0; i < 8; i++ {
		users = append(users, testutil.Member(t, s, a, fmt.Sprintf("redeem%d@test.invalid", i), "member"))
	}
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 12345, MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func(u store.User) {
			defer wg.Done()
			if amount, e := s.Redeem(ctx, u.ID, codes[0]); e == nil {
				if amount != 12345 {
					t.Error("wrong amount")
				}
				successes.Add(1)
			}
		}(u)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("single-use code overspent", successes.Load())
	}
	var sum int64
	s.Pool.QueryRow(ctx, "SELECT sum(balance) FROM users").Scan(&sum)
	if sum != 12345 {
		t.Fatal("wrong redeemed total", sum)
	}
	list, _, err := s.Codes(ctx, 1, 20)
	if err != nil || list[0].UsedCount != 1 {
		t.Fatal("wrong use count", err)
	}
	multi, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 1000, MaxUses: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, users[0].ID, multi[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, users[0].ID, multi[0]); err == nil {
		t.Fatal("same user redeemed twice")
	}
	if _, err = s.Redeem(ctx, users[1].ID, multi[0]); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	blocked, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 100, MaxUses: 2, ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	var codeID string
	s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(blocked[0])).Scan(&codeID)
	if err = s.DisableCode(ctx, a.ID, codeID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, users[0].ID, blocked[0]); err == nil {
		t.Fatal("disabled code accepted")
	}
	if err = s.DisableCode(ctx, a.ID, codeID, false); err != nil {
		t.Fatal(err)
	}
	s.Pool.Exec(ctx, "UPDATE redeem_codes SET expires_at=now()-interval '1 second' WHERE id=$1", codeID)
	if _, err = s.Redeem(ctx, users[0].ID, blocked[0]); err == nil {
		t.Fatal("expired code accepted")
	}
}
func TestPermissionsAccountDisableAndRoleChanges(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "member@test.invalid", "member")
	if u.Can("admin.users.manage") || u.Can("admin.refunds.read") {
		t.Fatal("member has admin permission")
	}
	if err := s.AdminCredit(ctx, u.ID, u.ID, "forged-credit", 10000, "forgery"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("store accepted forged administrator", err)
	}
	if err := s.AdminUpdateUser(ctx, a.ID, a.ID, store.UserInput{Name: a.Name, Role: "member", ServiceFee: 3000}); err == nil {
		t.Fatal("self demotion accepted")
	}
	session, err := s.NewSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateKey(ctx, u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdminUpdateUser(ctx, a.ID, u.ID, store.UserInput{Name: u.Name, Role: "operator", ServiceFee: 5000}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, session, false); err == nil {
		t.Fatal("session survived role change")
	}
	u, err = s.Authenticate(ctx, key, true)
	if err != nil || !u.Can("admin.refunds.read") || u.Can("admin.users.manage") {
		t.Fatal("role permissions incorrect", u, err)
	}
	if err = s.AdminUpdateUser(ctx, a.ID, u.ID, store.UserInput{Name: u.Name, Role: "operator", ServiceFee: 5000, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, key, true); err == nil {
		t.Fatal("disabled user's API key still works")
	}
	if _, err = s.Login(ctx, u.Email, "TestPassword-12345"); err == nil {
		t.Fatal("disabled user logged in")
	}
}

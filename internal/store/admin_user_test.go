package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAdminEditEmailNameAndBootstrapDoesNotRecreateOldLogin(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "edit@test.invalid", "member")
	ctx := context.Background()
	session, _ := s.NewSession(ctx, u.ID)
	key, _ := s.CreateKey(ctx, u.ID, "existing integration")
	in := store.UserInput{Email: " NEW@Test.Invalid ", Name: " 新称呼 ", Role: "member", ServiceFee: 4500}
	if err := s.AdminUpdateUser(ctx, a.ID, u.ID, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Login(ctx, "new@test.invalid", "TestPassword-12345")
	if err != nil || got.ID != u.ID || got.Name != "新称呼" || got.ServiceFee != 4500 {
		t.Fatal(got, err)
	}
	if _, err = s.Login(ctx, u.Email, "TestPassword-12345"); err == nil {
		t.Fatal("old email still logs in")
	}
	if _, err = s.Authenticate(ctx, session, false); err == nil {
		t.Fatal("email edit did not revoke sessions")
	}
	if _, err = s.Authenticate(ctx, key, true); err != nil {
		t.Fatal("email edit broke existing API key", err)
	}
	for _, email := range []string{a.Email, "broken-address", "Name <name@test.invalid>"} {
		in.Email = email
		if err = s.AdminUpdateUser(ctx, a.ID, u.ID, in); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("bad email accepted", email, err)
		}
	}
	got, _ = s.User(ctx, u.ID)
	if got.Email != "new@test.invalid" {
		t.Fatal("failed update changed email")
	}
	if err = s.AdminUpdateUser(ctx, a.ID, a.ID, store.UserInput{Email: "renamed-admin@test.invalid", Name: "New admin", Role: "admin", ServiceFee: a.ServiceFee}); err != nil {
		t.Fatal(err)
	}
	if err = s.Bootstrap(ctx, a.Email, "TestPassword-12345", "Old admin"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email=$1", a.Email).Scan(&count); err != nil || count != 0 {
		t.Fatal("old bootstrap account recreated", err)
	}
	if _, err = s.Login(ctx, "renamed-admin@test.invalid", "TestPassword-12345"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminDeleteUserRetainsHistoryRevokesAccessAndCannotRestore(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "delete@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "delete-funding", 10000, "test credit"); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "delete-history", []refundprovider.Account{account("delete-refund@test.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdminDeleteUser(ctx, a.ID, u.ID); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("active refund deletion accepted", err)
	}
	if err = s.Transition(ctx, u.ID, ids[0], "cancelled", "申请已取消，冻结积分已释放。"); err != nil {
		t.Fatal(err)
	}
	session, _ := s.NewSession(ctx, u.ID)
	key, _ := s.CreateKey(ctx, u.ID, "to revoke")
	for range 2 {
		if err = s.AdminDeleteUser(ctx, a.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	users, total, err := s.AdminUsers(ctx, u.Email, 1, 100)
	if err != nil || total != 0 || len(users) != 0 {
		t.Fatal("deleted user listed", err)
	}
	for _, isKey := range []bool{false, true} {
		token := session
		if isKey {
			token = key
		}
		if _, err = s.Authenticate(ctx, token, isKey); err == nil {
			t.Fatal("deleted access accepted")
		}
	}
	if _, err = s.Login(ctx, u.Email, "TestPassword-12345"); err == nil {
		t.Fatal("deleted login accepted")
	}
	if err = s.AdminUpdateUser(ctx, a.ID, u.ID, store.UserInput{Name: u.Name, Role: u.Role, ServiceFee: u.ServiceFee}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted user restored", err)
	}
	if err = s.AdminCredit(ctx, a.ID, u.ID, "after-deletion", 100, "not allowed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted user credited", err)
	}
	if err = s.AdminPassword(ctx, a.ID, u.ID, "NewPassword-12345"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted password reset", err)
	}
	if err = s.Credit(ctx, u.Email, 100, "not allowed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted user credited through CLI", err)
	}
	if _, err = s.NewSession(ctx, u.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("deleted user issued a session", err)
	}
	if _, err = s.CreateKey(ctx, u.ID, "late key"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("deleted user issued an API key", err)
	}
	if _, _, err = s.CreateRefunds(ctx, u.ID, "after-deletion-request", []refundprovider.Account{account("late@test.invalid")}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("late submission accepted", err)
	}
	var balance int64
	var refunds, entries, logs int
	if err = s.Pool.QueryRow(ctx, `SELECT balance,(SELECT count(*) FROM refunds WHERE user_id=$1),(SELECT count(*) FROM ledger WHERE user_id=$1),(SELECT count(*) FROM audit_logs WHERE action='user.delete' AND target_id=$1) FROM users WHERE id=$1 AND deleted_at IS NOT NULL`, u.ID).Scan(&balance, &refunds, &entries, &logs); err != nil {
		t.Fatal(err)
	}
	if balance != 10000 || refunds != 1 || entries != 3 || logs != 1 {
		t.Fatal("history changed or duplicate audit", balance, refunds, entries, logs)
	}
	if err = s.AdminDeleteUser(ctx, a.ID, a.ID); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("self deletion allowed", err)
	}
}

func TestAdminDeleteUserRejectsZeroFeeActiveRequestAndNonAdmin(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "zero@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminUpdateUser(ctx, a.ID, u.ID, store.UserInput{Name: u.Name, Role: u.Role, ServiceFee: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRefunds(ctx, u.ID, "zero-fee-request", []refundprovider.Account{account("zero-refund@test.invalid")}); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminDeleteUser(ctx, a.ID, u.ID); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("zero fee active request lost", err)
	}
	if err := s.AdminDeleteUser(ctx, u.ID, a.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("non-admin delete accepted", err)
	}
}

func TestAdminDeleteUserSerializesWithNewRefund(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "race@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "race-funding", 10000, "test"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var deleteErr, submitErr error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; deleteErr = s.AdminDeleteUser(ctx, a.ID, u.ID) }()
	go func() {
		defer wg.Done()
		<-start
		_, _, submitErr = s.CreateRefunds(ctx, u.ID, "concurrent-request", []refundprovider.Account{account("race-refund@test.invalid")})
	}()
	close(start)
	wg.Wait()
	if deleteErr == nil && !errors.Is(submitErr, store.ErrForbidden) {
		t.Fatal("submission survived deletion", submitErr)
	}
	if submitErr == nil && !errors.Is(deleteErr, store.ErrInvalid) {
		t.Fatal("deleted active account", deleteErr)
	}
	if deleteErr != nil && submitErr != nil {
		t.Fatal("neither operation completed", deleteErr, submitErr)
	}
}

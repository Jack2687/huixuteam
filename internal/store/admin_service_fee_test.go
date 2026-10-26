package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAdminServiceFeeBatchPreservesReservedAndSettledFees(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "price-first@test.invalid", "member")
	v := testutil.Member(t, s, a, "price-second@test.invalid", "operator")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "price-funding", 20000, "test funding"); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "price-before", []refundprovider.Account{account("price-queued@test.invalid"), account("price-settled@test.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"processing", "succeeded"} {
		if err = s.Transition(ctx, u.ID, ids[1], status, ""); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.User(ctx, u.ID)
	session, err := s.NewSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // A retry of the same price must not duplicate change records.
		if err = s.AdminSetServiceFees(ctx, a.ID, []string{v.ID, u.ID}, 1250); err != nil {
			t.Fatal(err)
		}
	}
	for _, original := range []store.User{u, v} {
		got, err := s.User(ctx, original.ID)
		if err != nil || got.ServiceFee != 1250 || got.Email != original.Email || got.Name != original.Name || got.Role != original.Role || got.Disabled != original.Disabled {
			t.Fatal("non-price fields changed", got, err)
		}
	}
	after, _ := s.User(ctx, u.ID)
	if after.Balance != before.Balance || after.Frozen != before.Frozen {
		t.Fatal("batch adjustment changed account funds", before, after)
	}
	if _, err = s.Authenticate(ctx, session, false); err != nil {
		t.Fatal("price change revoked session", err)
	}
	for _, id := range ids {
		r, err := s.Refund(ctx, u.ID, id)
		if err != nil || r.Fee != 3000 {
			t.Fatal("historical price changed", r, err)
		}
	}
	newIDs, _, err := s.CreateRefunds(ctx, u.ID, "price-after", []refundprovider.Account{account("price-new@test.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Refund(ctx, u.ID, newIDs[0])
	if err != nil || r.Fee != 1250 {
		t.Fatal("new request did not use new price", r, err)
	}
	for _, status := range []string{"processing", "succeeded"} {
		if err = s.Transition(ctx, u.ID, ids[0], status, ""); err != nil {
			t.Fatal(err)
		}
	}
	after, _ = s.User(ctx, u.ID)
	if after.Balance != 14000 || after.Frozen != 1250 {
		t.Fatal("existing request not settled at its original fee", after)
	}
	var logs, batches int
	err = s.Pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT metadata->>'batch_id') FROM audit_logs WHERE action='user.service_fee' AND actor_id=$1 AND target_id=ANY($2) AND metadata->>'before'='3000' AND metadata->>'after'='1250'`, a.ID, []string{u.ID, v.ID}).Scan(&logs, &batches)
	if err != nil || logs != 2 || batches != 1 {
		t.Fatal("missing or repeated per-account audit", logs, batches, err)
	}
	admin, _ := s.User(ctx, a.ID)
	if admin.ServiceFee != a.ServiceFee {
		t.Fatal("unselected account changed")
	}
}

func TestAdminServiceFeeBatchValidationAndRollback(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "price-valid@test.invalid", "member")
	deleted := testutil.Member(t, s, a, "price-deleted@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminDeleteUser(ctx, a.ID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{nil, {}, {u.ID, u.ID}, {u.ID, "missing"}, {u.ID, deleted.ID}, {" "}, {" " + u.ID}, make([]string, 1001)} {
		if err := s.AdminSetServiceFees(ctx, a.ID, ids, 9900); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid selection accepted", err)
		}
	}
	for _, fee := range []int64{-1, 10000001} {
		if err := s.AdminSetServiceFees(ctx, a.ID, []string{u.ID}, fee); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid fee accepted", err)
		}
	}
	if err := s.AdminSetServiceFees(ctx, u.ID, []string{a.ID}, 1); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member altered price", err)
	}
	got, _ := s.User(ctx, u.ID)
	var logs int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='user.service_fee'").Scan(&logs); err != nil || logs != 0 || got.ServiceFee != 3000 {
		t.Fatal("failed batch partially committed", got, logs, err)
	}
	for _, fee := range []int64{0, 10000000} {
		if err := s.AdminSetServiceFees(ctx, a.ID, []string{u.ID}, fee); err != nil {
			t.Fatal("fee boundary rejected", err)
		}
	}
}

func TestAdminServiceFeeSerializesWithNewRequests(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "price-concurrent@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "price-concurrent-funding", 100000, "test"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 9)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); <-start; errs <- s.AdminSetServiceFees(ctx, a.ID, []string{u.ID}, 1500) }()
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, err := s.CreateRefunds(ctx, u.ID, fmt.Sprintf("concurrent-%d", i), []refundprovider.Account{account(fmt.Sprintf("price-%d@test.invalid", i))})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count, invalid int
	var frozen, fees int64
	err := s.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE fee NOT IN (1500,3000)),sum(fee),(SELECT frozen FROM users WHERE id=$1) FROM refunds WHERE user_id=$1`, u.ID).Scan(&count, &invalid, &fees, &frozen)
	if err != nil || count != 8 || invalid != 0 || frozen != fees {
		t.Fatal("reservation and recorded price diverged", count, invalid, fees, frozen, err)
	}
}

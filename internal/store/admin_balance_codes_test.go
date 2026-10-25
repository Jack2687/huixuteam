package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAdminDeductionPreservesFrozenAndIdempotency(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "adjust@test.invalid", "member")
	ctx := context.Background()
	if err := s.AdminCredit(ctx, a.ID, u.ID, "fund-adjustment", 10000, "funding"); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "freeze-adjustment", []refundprovider.Account{account("adjust-refund@test.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.AdminCredit(ctx, a.ID, u.ID, "deduct-replay", -2000, "correction"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.AdminCredit(ctx, a.ID, u.ID, "deduct-replay", -2001, "correction"); !errors.Is(err, store.ErrConflict) {
		t.Fatal("missing idempotency conflict", err)
	}
	if err = s.AdminCredit(ctx, a.ID, u.ID, "deduct-frozen", -5001, "too much"); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("deducted reserved funds", err)
	}
	if err = s.AdminCredit(ctx, u.ID, u.ID, "deduct-forged", -1, "forged"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member deduction allowed", err)
	}
	for _, amount := range []int64{0, -100000001, 100000001, -9223372036854775808} {
		if err = s.AdminCredit(ctx, a.ID, u.ID, "deduct-invalid", amount, "invalid"); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid amount allowed", err)
		}
	}
	// Two concurrent admins cannot deduct more than the available 50 points.
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := s.AdminCredit(ctx, a.ID, u.ID, fmt.Sprintf("deduct-concurrent-%d", i), -4000, "correction")
			if e == nil {
				successes.Add(1)
			} else if !errors.Is(e, store.ErrInvalid) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent overdraft protection failed")
	}
	if err = s.AdminCredit(ctx, a.ID, u.ID, "deduct-to-frozen", -1000, "correction"); err != nil {
		t.Fatal(err)
	}
	u, err = s.User(ctx, u.ID)
	if err != nil || u.Balance != 3000 || u.Frozen != 3000 {
		t.Fatal("frozen balance changed", u, err)
	}
	entries, _, err := s.Billing(ctx, u.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	var debits int
	for _, e := range entries {
		if e.Kind == "debit" {
			sum += e.Amount
			debits++
		}
	}
	if sum != -7000 || debits != 3 {
		t.Fatal("incorrect debit ledger", sum, debits)
	}
	if err = s.Transition(ctx, u.ID, ids[0], "processing", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Transition(ctx, u.ID, ids[0], "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	u, _ = s.User(ctx, u.ID)
	if u.Balance != 0 || u.Frozen != 0 {
		t.Fatal("reserved settlement broken", u)
	}
}

func TestCodeExportDeletionAndHistory(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "code-management@test.invalid", "member")
	ctx := context.Background()
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 3, Amount: 1000, MaxUses: 2})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(codes))
	for i, code := range codes {
		var secret []byte
		if err = s.Pool.QueryRow(ctx, "SELECT id,code_secret FROM redeem_codes WHERE code_hash=$1", store.Hash(code)).Scan(&ids[i], &secret); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(secret, []byte(code)) || len(secret) == 0 {
			t.Fatal("full code not encrypted")
		}
	}
	listed, total, err := s.Codes(ctx, 1, 100)
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
	for _, c := range listed {
		if !c.Exportable {
			t.Fatal("new code not exportable")
		}
	}
	raw, _ := json.Marshal(listed)
	if bytes.Contains(raw, []byte(codes[0])) {
		t.Fatal("list leaks full code")
	}
	exported, err := s.ExportCodes(ctx, a.ID, []string{ids[2], ids[0]})
	if err != nil || len(exported) != 2 || exported[0] != codes[2] || exported[1] != codes[0] {
		t.Fatal("selected export mismatch", err)
	}
	if _, err = s.ExportCodes(ctx, u.ID, ids); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member export allowed", err)
	}
	if err = s.DeleteCodes(ctx, u.ID, ids); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member deletion allowed", err)
	}
	for _, selection := range [][]string{nil, {ids[0], ids[0]}, {ids[0], "missing"}} {
		if err = s.DeleteCodes(ctx, a.ID, selection); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid deletion allowed", err)
		}
		if _, err = s.ExportCodes(ctx, a.ID, selection); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid export allowed", err)
		}
	}
	if _, err = s.Redeem(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.DeleteCodes(ctx, a.ID, ids[:2]); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err = s.Codes(ctx, 1, 100)
	if err != nil || total != 1 {
		t.Fatal("deleted codes still visible", total, err)
	}
	if _, err = s.Redeem(ctx, u.ID, codes[1]); err == nil {
		t.Fatal("deleted code still redeemable")
	}
	if err = s.DisableCode(ctx, a.ID, ids[0], false); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted code re-enabled", err)
	}
	if _, err = s.ExportCodes(ctx, a.ID, ids); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("deleted code exported", err)
	}
	history, count, err := s.Redemptions(ctx, 1, 100)
	if err != nil || count != 1 || history[0].Amount != 1000 {
		t.Fatal("redemption history lost", err)
	}
	u, _ = s.User(ctx, u.ID)
	if u.Balance != 1000 {
		t.Fatal("deletion changed credited balance")
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE redeem_codes SET code_secret=NULL WHERE id=$1", ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExportCodes(ctx, a.ID, ids[2:]); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("hash-only legacy code fabricated", err)
	}
	listed, _, _ = s.Codes(ctx, 1, 100)
	if listed[0].Exportable {
		t.Fatal("legacy code marked exportable")
	}
	if _, err = s.Redeem(ctx, u.ID, codes[2]); err != nil {
		t.Fatal("legacy redemption broken", err)
	}
	audits, _, err := s.Audits(ctx, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(audits)
	for _, c := range codes {
		if bytes.Contains(raw, []byte(c)) {
			t.Fatal("audit leaks code")
		}
	}
}

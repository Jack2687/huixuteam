package store_test

import (
	"context"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"os"
	"testing"
)

func TestSingleUseLegacyCodesAndMigration(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "single-use@test.invalid", "member")
	other := testutil.Member(t, s, a, "single-use-other@test.invalid", "member")
	for _, n := range []int{-1, 2, 10000} {
		if _, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 1000, MaxUses: n}); err == nil {
			t.Fatalf("accepted max_uses=%d", n)
		}
	}
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 3, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 3)
	for i, code := range codes {
		if err = s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(code)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Pool.Exec(ctx, "UPDATE redeem_codes SET max_uses=5 WHERE id=$1", ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Legacy unused code may be consumed once even before schema normalization.
	if _, err = s.Redeem(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, other.ID, codes[0]); err == nil {
		t.Fatal("legacy code allowed a second user")
	}
	if err = s.DisableCode(ctx, a.ID, ids[0], false); err == nil {
		t.Fatal("used code could be re-enabled")
	}
	// Model historical multiple redemptions without rewriting their original amounts.
	if _, err = s.Pool.Exec(ctx, "UPDATE redeem_codes SET used_count=2 WHERE id=$1", ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, "INSERT INTO redemptions(code_id,user_id,amount) VALUES($1,$2,1000),($1,$3,1000)", ids[1], u.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		t.Helper()
		var h string
		err := s.Pool.QueryRow(ctx, `SELECT md5((SELECT jsonb_agg(to_jsonb(u) ORDER BY id)::text FROM users u)||(SELECT jsonb_agg(to_jsonb(l) ORDER BY id)::text FROM ledger l)||(SELECT jsonb_agg(to_jsonb(r) ORDER BY id)::text FROM redemptions r))`).Scan(&h)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	before := fingerprint()
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.Pool.Exec(ctx, string(schema)); err != nil {
			t.Fatal(err)
		}
	}
	if fingerprint() != before {
		t.Fatal("migration changed users, ledger or redemption history")
	}
	var max, used int
	if err = s.Pool.QueryRow(ctx, "SELECT max_uses,used_count FROM redeem_codes WHERE id=$1", ids[1]).Scan(&max, &used); err != nil || max != 5 || used != 2 {
		t.Fatal("historical counts changed", err)
	}
	if err = s.Pool.QueryRow(ctx, "SELECT max_uses FROM redeem_codes WHERE id=$1", ids[2]).Scan(&max); err != nil || max != 1 {
		t.Fatal("unused code not normalized", err)
	}
	if _, err = s.Redeem(ctx, a.ID, codes[1]); err == nil {
		t.Fatal("historically consumed code accepted")
	}
	if _, err = s.Redeem(ctx, other.ID, codes[2]); err != nil {
		t.Fatal(err)
	}
}

func TestSingleUseLedgerFailureRollsBack(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "single-rollback@test.invalid", "member")
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_redeem_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER reject_redeem_ledger BEFORE INSERT ON ledger FOR EACH ROW EXECUTE FUNCTION reject_redeem_ledger()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, u.ID, codes[0]); err == nil {
		t.Fatal("expected ledger failure")
	}
	var balance int64
	var used, history int
	s.Pool.QueryRow(ctx, "SELECT balance FROM users WHERE id=$1", u.ID).Scan(&balance)
	s.Pool.QueryRow(ctx, "SELECT used_count FROM redeem_codes WHERE code_hash=$1", store.Hash(codes[0])).Scan(&used)
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM redemptions WHERE user_id=$1", u.ID).Scan(&history)
	if balance != 0 || used != 0 || history != 0 {
		t.Fatal("partial redemption survived rollback")
	}
	if _, err = s.Pool.Exec(ctx, "DROP TRIGGER reject_redeem_ledger ON ledger; DROP FUNCTION reject_redeem_ledger()"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
}

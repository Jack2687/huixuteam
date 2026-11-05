package store_test

import (
	"context"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"os"
	"testing"
)

func rescaleHistory(t *testing.T, s *store.Store, optIn bool) error {
	t.Helper()
	raw, err := os.ReadFile("../../scripts/finance-history-x10-20261105.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := s.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	defer c.Exec(ctx, "ROLLBACK; RESET huixu.allow_finance_rescale")
	if optIn {
		if _, err = c.Exec(ctx, "SET huixu.allow_finance_rescale='20261105'"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = c.Exec(ctx, string(raw))
	return err
}

func TestHistoricalFinanceRescaleAndFutureOperations(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "history@test.invalid", "member")
	other := testutil.Member(t, s, a, "redeemer@test.invalid", "member")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AdminCredit(ctx, a.ID, u.ID, "fund-history", 20001, "fixture"))
	must(s.AdminCredit(ctx, a.ID, u.ID, "debit-history", -101, "fixture"))
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 123, MaxUses: 2})
	must(err)
	_, err = s.Redeem(ctx, u.ID, codes[0])
	must(err)
	accounts := []refundprovider.Account{account("settled@test.invalid"), account("released@test.invalid"), account("queued-release@test.invalid"), account("queued-settle@test.invalid")}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "history-orders", accounts)
	must(err)
	must(s.Transition(ctx, u.ID, ids[0], "processing", "test"))
	must(s.Transition(ctx, u.ID, ids[0], "succeeded", "test"))
	must(s.Transition(ctx, u.ID, ids[1], "cancelled", "test"))
	before, err := s.User(ctx, u.ID)
	must(err)
	var oldAudit string
	must(s.Pool.QueryRow(ctx, "SELECT md5(string_agg(to_jsonb(a)::text,'' ORDER BY id)) FROM audit_logs a").Scan(&oldAudit))
	if rescaleHistory(t, s, false) == nil {
		t.Fatal("migration ran without maintenance opt-in")
	}
	must(rescaleHistory(t, s, true))
	after, err := s.User(ctx, u.ID)
	must(err)
	if after.Balance != before.Balance*10 || after.Frozen != before.Frozen*10 || after.ServiceFee != before.ServiceFee*10 {
		t.Fatal("incorrect migration", before, after)
	}
	var audit string
	must(s.Pool.QueryRow(ctx, "SELECT md5(string_agg(to_jsonb(a)::text,'' ORDER BY id)) FROM audit_logs a WHERE action<>'finance.history_rescale'").Scan(&audit))
	if audit != oldAudit {
		t.Fatal("historical audit changed")
	}
	// Old admin and customer retries must still be recognized without charging again.
	must(s.AdminCredit(ctx, a.ID, u.ID, "fund-history", 20001, "fixture"))
	got, replayed, err := s.CreateRefunds(ctx, u.ID, "history-orders", accounts)
	must(err)
	if !replayed || got[0] != ids[0] {
		t.Fatal("request idempotency lost")
	}
	must(s.Transition(ctx, u.ID, ids[2], "cancelled", "after migration"))
	must(s.Transition(ctx, u.ID, ids[3], "processing", "test"))
	must(s.Transition(ctx, u.ID, ids[3], "succeeded", "test"))
	must(s.Transition(ctx, u.ID, ids[3], "succeeded", "duplicate"))
	current, err := s.User(ctx, u.ID)
	must(err)
	if current.Frozen != 0 || current.Balance != after.Balance-30000 {
		t.Fatal("queued release/settlement incorrect", current)
	}
	amount, err := s.Redeem(ctx, other.ID, codes[0])
	must(err)
	if amount != 1230 {
		t.Fatal("old code amount not scaled", amount)
	}
	must(s.AdminCredit(ctx, a.ID, u.ID, "future-credit", 1000, "new ten credits"))
	current, err = s.User(ctx, u.ID)
	must(err)
	if current.Balance != after.Balance-30000+1000 {
		t.Fatal("future credit incorrectly scaled")
	}
	// Repeated migration must leave even NEW transactions and prices untouched.
	must(rescaleHistory(t, s, true))
	repeated, err := s.User(ctx, u.ID)
	must(err)
	if repeated.Balance != current.Balance || repeated.Frozen != current.Frozen || repeated.ServiceFee != current.ServiceFee {
		t.Fatal("migration applied twice")
	}
	cfg, err := s.SiteService(ctx)
	must(err)
	if cfg.DefaultServiceFee != 10000 {
		t.Fatal("new default scaled")
	}
	var mismatches int
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM users u WHERE frozen<>(SELECT COALESCE(sum(fee),0) FROM refunds r WHERE r.user_id=u.id AND fee_status='frozen')").Scan(&mismatches))
	if mismatches != 0 {
		t.Fatal("freeze invariant broken")
	}
	var n int
	must(s.Pool.QueryRow(ctx, "SELECT count(*) FROM finance_data_migrations").Scan(&n))
	if n != 1 {
		t.Fatal("migration marker missing")
	}
}

func TestHistoryMigrationRejectsMismatchActiveAndOverflow(t *testing.T) {
	for _, scenario := range []string{"freeze", "active", "overflow", "settlement", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s := testutil.Database(t)
			a := testutil.Admin(t, s)
			ctx := context.Background()
			u := testutil.Member(t, s, a, "guard@test.invalid", "member")
			if err := s.Credit(ctx, u.Email, 10000, "test"); err != nil {
				t.Fatal(err)
			}
			var err error
			switch scenario {
			case "write_failure":
				_, err = s.Pool.Exec(ctx, `CREATE FUNCTION fail_ledger_rescale() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected migration failure'; END $$; CREATE TRIGGER reject_rescale BEFORE UPDATE ON ledger FOR EACH ROW EXECUTE FUNCTION fail_ledger_rescale()`)
			case "freeze":
				_, err = s.Pool.Exec(ctx, "UPDATE users SET frozen=1 WHERE id=$1", u.ID)
			case "overflow":
				_, err = s.Pool.Exec(ctx, "UPDATE users SET balance=900719925474100 WHERE id=$1", u.ID)
			default:
				ids, _, e := s.CreateRefunds(ctx, u.ID, "guard-order", []refundprovider.Account{account("guard-order@test.invalid")})
				if e != nil {
					t.Fatal(e)
				}
				if scenario == "active" {
					err = s.Transition(ctx, u.ID, ids[0], "processing", "test")
				} else {
					err = s.Transition(ctx, u.ID, ids[0], "processing", "test")
					if err == nil {
						err = s.Transition(ctx, u.ID, ids[0], "succeeded", "test")
					}
					if err == nil {
						_, err = s.Pool.Exec(ctx, "UPDATE ledger SET amount=amount-1 WHERE refund_id=$1 AND kind='settlement'", ids[0])
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := s.User(ctx, u.ID)
			if err = rescaleHistory(t, s, true); err == nil {
				t.Fatal("invalid migration accepted")
			}
			after, _ := s.User(ctx, u.ID)
			if before.Balance != after.Balance || before.Frozen != after.Frozen || before.ServiceFee != after.ServiceFee {
				t.Fatal("partial changes committed")
			}
		})
	}
}

package store_test

import (
	"context"
	"testing"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAccountReportCalendar(t *testing.T) {
	for _, tc := range []struct{ period, anchor, start, end string }{
		{"day", "2024-02-29", "2024-02-29", "2024-03-01"},
		{"week", "2021-01-01", "2020-12-28", "2021-01-04"},
		{"week", "2026-10-25", "2026-10-19", "2026-10-26"},
		{"month", "2024-02-29", "2024-02-01", "2024-03-01"},
		{"year", "2024-12-31", "2024-01-01", "2025-01-01"},
		{"day", "", "2026-10-27", "2026-10-28"},
	} {
		start, end, err := store.AccountReportPeriod(tc.period, tc.anchor, time.Date(2026, 10, 26, 16, 30, 0, 0, time.UTC))
		_, offset := start.Zone()
		if err != nil || start.Format("2006-01-02") != tc.start || end.Format("2006-01-02") != tc.end || offset != 28800 {
			t.Fatalf("%+v: %v %v %v", tc, start, end, err)
		}
	}
	for _, tc := range []struct{ p, d string }{{"hour", "2026-10-26"}, {"day", "2026-02-29"}, {"day", "bad"}, {"year", "9999-01-01"}} {
		if _, _, err := store.AccountReportPeriod(tc.p, tc.d, time.Now()); err == nil {
			t.Fatal("invalid period accepted", tc)
		}
	}
}

func TestAccountReportLedgerAndApplications(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "report@test.invalid", "member")
	testutil.Member(t, s, a, "empty@test.invalid", "member")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.AdminCredit(ctx, a.ID, u.ID, "report-fund", 20000, "funding"))
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 2000, MaxUses: 1})
	must(err)
	_, err = s.Redeem(ctx, u.ID, codes[0])
	must(err)
	must(s.AdminCredit(ctx, a.ID, u.ID, "report-deduct", -500, "correction"))
	must(s.AdminCredit(ctx, a.ID, a.ID, "admin-fund", 9999, "funding"))
	accounts := []refundprovider.Account{account("previous@test.invalid"), account("success@test.invalid"), account("queue@test.invalid"), account("review@test.invalid"), account("cancel@test.invalid")}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "report-requests", accounts)
	must(err)
	for _, i := range []int{0, 1, 3} {
		must(s.Transition(ctx, u.ID, ids[i], "processing", ""))
	}
	for _, i := range []int{0, 1} {
		must(s.Transition(ctx, u.ID, ids[i], "succeeded", ""))
	}
	must(s.Transition(ctx, u.ID, ids[3], "attention", ""))
	must(s.Transition(ctx, u.ID, ids[4], "cancelled", ""))
	// A changed price must not rewrite old fees or frozen requests.
	must(s.AdminUpdateUser(ctx, a.ID, u.ID, store.UserInput{Name: u.Name, Role: u.Role, ServiceFee: 4500}))
	_, err = s.Pool.Exec(ctx, "UPDATE ledger SET created_at='2026-10-26 04:00:00+00'; UPDATE refunds SET created_at='2026-10-26 04:00:00+00'")
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at='2026-10-25 15:59:59+00' WHERE id=$1", ids[0])
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE ledger SET created_at='2026-10-25 15:59:59+00' WHERE refund_id=$1 AND kind='freeze'", ids[0])
	must(err)
	// Half-open Beijing day: include midnight, exclude the next midnight.
	_, err = s.Pool.Exec(ctx, "UPDATE ledger SET created_at='2026-10-25 16:00:00+00' WHERE user_id=$1 AND kind='redeem'", u.ID)
	must(err)
	for _, c := range []struct {
		key, ts string
		value   int64
	}{{"before", "2026-10-25 15:59:59+00", 111}, {"after", "2026-10-26 16:00:00+00", 222}} {
		must(s.AdminCredit(ctx, a.ID, u.ID, "boundary-"+c.key, c.value, "boundary"))
		_, err = s.Pool.Exec(ctx, "UPDATE ledger SET created_at=$2::timestamptz WHERE user_id=$1 AND kind='credit' AND amount=$3", u.ID, c.ts, c.value)
		must(err)
	}
	r, err := s.AdminAccountReport(ctx, "day", "2026-10-26", "", true, 1, 1)
	must(err)
	want := store.AccountReportMetrics{Recharged: 22000, Debited: 500, FreezeAdded: 12000, Released: 3000, Used: 6000, SettledCount: 2, Balance: 15833, Frozen: 6000, Available: 9833, Applications: 4, Queued: 1, Succeeded: 1, Attention: 1, Cancelled: 1}
	if r.Total != 2 || len(r.Items) != 1 || r.Summary != want || r.Items[0].AccountReportMetrics != want || r.Items[0].ServiceFee != 4500 {
		t.Fatalf("unexpected report: %+v", r)
	}
	r, err = s.AdminAccountReport(ctx, "day", "2026-10-26", "", true, 2, 1)
	must(err)
	if r.Total != 2 || r.Summary != want || len(r.Items) != 1 || r.Items[0].Applications != 0 {
		t.Fatalf("pagination changed totals: %+v", r)
	}
	r, err = s.AdminAccountReport(ctx, "day", "2026-10-26", "", true, 999, 1)
	must(err)
	if len(r.Items) != 0 || r.Total != 2 || r.Summary != want {
		t.Fatal("empty page lost summary")
	}
	r, err = s.AdminAccountReport(ctx, "day", "2026-10-26", "admin@", true, 1, 100)
	must(err)
	if r.Total != 0 || len(r.Items) != 0 || r.Summary != (store.AccountReportMetrics{}) {
		t.Fatal("admin not excluded")
	}
	r, err = s.AdminAccountReport(ctx, "day", "2026-10-26", "ADMIN@", false, 1, 100)
	must(err)
	if r.Total != 1 || r.Summary.Recharged != 9999 || r.Summary.Balance != 9999 {
		t.Fatal("admin opt in/search failed", r)
	}
	r, err = s.AdminAccountReport(ctx, "month", "2026-10-26", "report@", true, 1, 100)
	must(err)
	if r.Summary.Recharged != 22333 || r.Summary.Applications != 5 || r.Summary.FreezeAdded != 15000 {
		t.Fatal("month aggregation failed", r)
	}
}

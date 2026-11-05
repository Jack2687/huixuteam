package store_test

import (
	"context"
	"encoding/json"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"strings"
	"testing"
)

func TestRedeemUsageBoundariesHistoryAndExactSearch(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "usage@test.invalid", "member")
	v := testutil.Member(t, s, a, "historical@test.invalid", "member")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 4, Amount: 1000, Note: "100%_literal"})
	must(err)
	ids := make([]string, 4)
	for i, code := range codes {
		must(s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(code)).Scan(&ids[i]))
		rows, total, e := s.Codes(ctx, "  "+strings.ToLower(code)+"  ", 1, 1)
		must(e)
		if total != 1 || len(rows) != 1 || rows[0].ID != ids[i] {
			t.Fatal("full-code search failed", total, rows)
		}
	}
	stamps := []string{"2026-11-04T15:59:59Z", "2026-11-04T16:00:00Z", "2026-11-05T15:59:59Z", "2026-11-05T16:00:00Z"}
	for i, stamp := range stamps {
		_, e := s.Pool.Exec(ctx, "INSERT INTO redemptions(code_id,user_id,amount,created_at) VALUES($1,$2,$3,$4::timestamptz)", ids[i], u.ID, (i+1)*1000, stamp)
		must(e)
	}
	_, err = s.Pool.Exec(ctx, "INSERT INTO redemptions(code_id,user_id,amount,created_at) VALUES($1,$2,1500,'2026-11-05T01:00:00Z')", ids[1], v.ID)
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE users SET disabled=true,deleted_at=now() WHERE id=$1", v.ID)
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE redeem_codes SET deleted_at=now() WHERE id=$1", ids[1])
	must(err)
	report, err := s.RedeemUsage(ctx, "day", "2026-11-05", "", 1, 1)
	must(err)
	if report.Total != 3 || report.Summary.Codes != 2 || report.Summary.Accounts != 2 || report.Summary.Amount != 6500 || len(report.Items) != 1 {
		t.Fatalf("bad summary: %+v", report)
	}
	next, err := s.RedeemUsage(ctx, "day", "2026-11-05", "", 2, 1)
	must(err)
	if next.Items[0].ID == report.Items[0].ID || next.Summary != report.Summary {
		t.Fatal("pagination changed summary")
	}
	for _, q := range []string{"100%_", strings.ToLower(codes[1]), v.Email, "absent"} {
		r, e := s.RedeemUsage(ctx, "day", "2026-11-05", q, 1, 50)
		must(e)
		expected := map[string]int{"100%_": 3, strings.ToLower(codes[1]): 2, v.Email: 1, "absent": 0}[q]
		if r.Total != expected {
			t.Fatal("search mismatch", q, r.Total)
		}
		raw, _ := json.Marshal(r)
		for _, code := range codes {
			if strings.Contains(string(raw), code) {
				t.Fatal("full code exposed")
			}
		}
	}
	for _, q := range []string{"%", "_"} {
		r, e := s.RedeemUsage(ctx, "day", "2026-11-05", q, 1, 50)
		must(e)
		if r.Total != 3 {
			t.Fatal("literal wildcard search")
		}
	}
	var balance int64
	must(s.Pool.QueryRow(ctx, "SELECT balance FROM users WHERE id=$1", u.ID).Scan(&balance))
	if balance != 0 {
		t.Fatal("report changed balance")
	}
	if _, e := s.RedeemUsage(ctx, "day", "2026-02-30", "", 1, 50); e == nil {
		t.Fatal("invalid date accepted")
	}
}

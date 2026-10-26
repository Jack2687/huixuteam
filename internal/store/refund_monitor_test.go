package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestRefundMonitorSummaryTimingAndFilters(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "monitor@test.invalid", "member")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Credit(ctx, u.Email, 100000, "fixture"))
	states := []string{"queued", "processing", "verifying", "attention", "succeeded", "failed", "banned", "cancelled"}
	accounts := []refundprovider.Account{}
	for _, state := range states {
		accounts = append(accounts, account(state+"@test.invalid"))
	}
	ids, _, err := s.CreateRefunds(ctx, u.ID, "monitor-fixture", accounts)
	must(err)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i, state := range states {
		fee := "frozen"
		if state == "succeeded" {
			fee = "settled"
		}
		if i >= 5 {
			fee = "released"
		}
		_, err = s.Pool.Exec(ctx, "UPDATE refunds SET status=$2,fee_status=$3,created_at=$4,updated_at=$5,message='private-canary export_and_deactivate' WHERE id=$1", ids[i], state, fee, base, base.Add(30*time.Minute))
		must(err)
		// Only business-visible start events count. Private starts must be ignored.
		_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal,created_at) VALUES($1,'开始处理',true,$2)", ids[i], base.Add(time.Second))
		must(err)
		if i >= 1 && i <= 5 {
			_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal,created_at) VALUES($1,'开始处理',false,$2),($1,'正在继续处理',false,$3)", ids[i], base.Add(5*time.Minute), base.Add(10*time.Minute))
			must(err)
		}
	}
	get := func(f store.RefundMonitorFilter) store.RefundMonitor {
		t.Helper()
		r, e := s.AdminRefundMonitor(ctx, f)
		must(e)
		return r
	}
	want := store.RefundMonitorSummary{Total: 8, Queued: 1, Running: 2, Attention: 1, Succeeded: 1, Failed: 2, Cancelled: 1, LongWait: 1, LongRunning: 2, Frozen: 12000, Settled: 3000, Released: 9000}
	r := get(store.RefundMonitorFilter{Size: 1})
	if r.Summary != want || r.Total != 8 || len(r.Items) != 1 || r.Items[0].Status != "attention" {
		t.Fatalf("priority/summary: %+v", r)
	}
	r = get(store.RefundMonitorFilter{Size: 1, Page: 999})
	if r.Summary != want || r.Total != 8 || len(r.Items) != 0 {
		t.Fatal("pagination lost totals", r)
	}
	r = get(store.RefundMonitorFilter{})
	if r.Size != 100 || len(r.Items) != 8 {
		t.Fatal("default page size", r.Size)
	}
	for _, row := range r.Items {
		switch row.Status {
		case "queued":
			if row.StartedAt != nil || row.HandleSeconds != nil || row.WaitSeconds == nil || *row.WaitSeconds < 3600 {
				t.Fatal("private start affected queued timing", row)
			}
		case "succeeded", "failed":
			if row.FinishedAt == nil || row.StartedAt == nil || *row.WaitSeconds != 300 || *row.HandleSeconds != 1500 {
				t.Fatal("terminal time not fixed", row)
			}
		case "banned":
			if row.StartedAt != nil || row.HandleSeconds != nil || row.WaitSeconds != nil {
				t.Fatal("invented historical timing", row)
			}
		case "cancelled":
			if row.StartedAt != nil || row.HandleSeconds != nil || *row.WaitSeconds != 1800 {
				t.Fatal("cancel wait incorrect", row)
			}
		}
	}
	raw, err := json.Marshal(r)
	must(err)
	for _, private := range []string{"private-canary", "export_and_deactivate", "credentials", "internal", "lease_token", "password"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("monitor privacy failure", private)
		}
	}
	for _, tc := range []struct {
		filter store.RefundMonitorFilter
		count  int
	}{
		{store.RefundMonitorFilter{Status: "running"}, 2}, {store.RefundMonitorFilter{Status: "unsuccessful"}, 2},
		{store.RefundMonitorFilter{Status: "abnormal"}, 3}, {store.RefundMonitorFilter{FeeStatus: "frozen"}, 4},
		{store.RefundMonitorFilter{Focus: "long_wait"}, 1}, {store.RefundMonitorFilter{Focus: "long_running"}, 2},
		{store.RefundMonitorFilter{Status: "succeeded", FeeStatus: "released"}, 0},
	} {
		r = get(tc.filter)
		if r.Total != tc.count || r.Summary != want {
			t.Fatalf("filter %+v: %+v", tc.filter, r)
		}
	}
	for _, q := range []string{u.Email, strings.ToUpper(u.Name)} {
		r = get(store.RefundMonitorFilter{Query: q})
		if r.Summary != want {
			t.Fatal("customer search", q)
		}
	}
	r = get(store.RefundMonitorFilter{Query: ids[4]})
	if r.Total != 1 || r.Items[0].ID != ids[4] || r.Summary.Settled != 3000 {
		t.Fatal("id search", r)
	}
	r = get(store.RefundMonitorFilter{Query: "nobody@"})
	if r.Total != 0 || r.Items == nil || r.Summary != (store.RefundMonitorSummary{}) {
		t.Fatal("empty search", r)
	}
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at=$2 WHERE id=$1", ids[0], base.Add(-time.Minute))
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at=$2,updated_at=$3 WHERE id=$1", ids[1], base.Add(time.Minute), base.Add(40*time.Minute))
	must(err)
	for sort, wanted := range map[string]string{"oldest": ids[0], "newest": ids[1], "updated": ids[1]} {
		r = get(store.RefundMonitorFilter{Sort: sort, Size: 1})
		if r.Items[0].ID != wanted {
			t.Fatal("incorrect ordering", sort)
		}
	}
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at='2026-10-25 15:59:59+00'")
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at='2026-10-25 16:00:00+00' WHERE id=$1", ids[0])
	must(err)
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at='2026-10-26 16:00:00+00' WHERE id=$1", ids[1])
	must(err)
	r = get(store.RefundMonitorFilter{Start: "2026-10-26", End: "2026-10-26"})
	if r.Total != 1 || r.Items[0].ID != ids[0] {
		t.Fatal("Beijing date boundaries", r)
	}
}

func TestRefundMonitorValidation(t *testing.T) {
	s := testutil.Database(t)
	for _, f := range []store.RefundMonitorFilter{{Status: "private"}, {FeeStatus: "bad"}, {Focus: "bad"}, {Sort: "bad"}, {Start: "2026-02-30"}, {End: "bad"}, {Start: "2026-10-27", End: "2026-10-26"}, {Query: strings.Repeat("a", 255)}} {
		if _, err := s.AdminRefundMonitor(context.Background(), f); err == nil {
			t.Fatalf("accepted invalid filter %+v", f)
		}
	}
}

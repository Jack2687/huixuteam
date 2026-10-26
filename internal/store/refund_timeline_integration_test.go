package store_test

import (
	"context"
	"testing"
)

func TestTimelineRepeatedCheckpointReadOnlyAndSettlement(t *testing.T) {
	s, _, u := executionFixture(t, 1)
	ids := submitRequests(t, s, u, 1)
	ctx := context.Background()
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"inviting", "invited", "login", "login", "merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted", "removing", "completed"} {
		checkpoint(t, s, j, stage)
	}
	var before, after string
	snapshot := `SELECT jsonb_build_object('refund',(SELECT to_jsonb(r) FROM refunds r WHERE id=$1),'execution',(SELECT to_jsonb(e) FROM refund_execution e WHERE refund_id=$1),'events',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM refund_events v WHERE refund_id=$1),'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger l WHERE refund_id=$1))::text`
	if err = s.Pool.QueryRow(ctx, snapshot, ids[0]).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		result, err := s.AdminRefundTimeline(ctx, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if result.Active {
			t.Fatal("finished active")
		}
		count := 0
		for _, r := range result.Items {
			if r.Stage == "登" {
				count++
			}
			if r.Status == "未记录" {
				t.Fatal("complete history missing", r)
			}
		}
		if count != 2 {
			t.Fatal("repeated login lost", count)
		}
	}
	if err = s.Pool.QueryRow(ctx, snapshot, ids[0]).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("timeline read mutated business data")
	}
}

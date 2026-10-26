// Business display fixtures in the isolated QA database; no upstream operations.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
)

func main() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	dsn := os.Getenv("DATABASE_URL")
	uri, err := url.Parse(dsn)
	must(err)
	if uri.Hostname() != "127.0.0.1" || uri.Port() != "18742" || uri.Path != "/llm_refund_qa" {
		panic("isolated QA database required")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("MASTER_KEY"))
	must(err)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn, key)
	must(err)
	defer s.Pool.Close()
	var enabled bool
	must(s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_settings WHERE id").Scan(&enabled))
	if enabled {
		panic("QA worker must remain disabled")
	}
	var actor string
	must(s.Pool.QueryRow(ctx, "SELECT id FROM users WHERE email=$1 AND role='admin'", os.Getenv("BOOTSTRAP_EMAIL")).Scan(&actor))
	email := "monitor-" + store.ID()[:10] + "@example.test"
	uid, err := s.AdminCreateUser(ctx, actor, store.UserInput{Email: email, Name: "监控验收客户", Password: "Monitor-Fixture-2026", Role: "member", ServiceFee: 3000})
	must(err)
	must(s.Credit(ctx, email, 30000, "QA monitor fixture"))
	rows := []map[string]string{}
	for i, status := range []string{"queued", "processing", "verifying", "attention", "succeeded", "failed", "banned", "cancelled"} {
		child := status + "-" + store.ID()[:8] + "@example.test"
		ids, _, err := s.CreateRefunds(ctx, uid, store.Token(), []refundprovider.Account{{Email: child, Password: "private-child-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
		must(err)
		if i > 0 && status != "cancelled" {
			must(s.Transition(ctx, uid, ids[0], "processing", "开始处理"))
		}
		if status != "queued" && status != "processing" {
			must(s.Transition(ctx, uid, ids[0], status, ""))
		}
		_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at=now()-interval '1 hour',updated_at=now()-interval '5 minutes',message='opaque-protocol-canary export_and_deactivate' WHERE id=$1", ids[0])
		must(err)
		_, err = s.Pool.Exec(ctx, "UPDATE refund_events SET created_at=now()-interval '30 minutes' WHERE refund_id=$1 AND message='开始处理'", ids[0])
		must(err)
		rows = append(rows, map[string]string{"id": ids[0], "email": child, "status": status})
	}
	loc := time.FixedZone("Beijing", 28800)
	raw, err := json.Marshal(map[string]any{"email": email, "rows": rows, "date": time.Now().In(loc).Format("2006-01-02")})
	must(err)
	must(os.WriteFile(".local/monitor-fixture.json", raw, 0600))
}

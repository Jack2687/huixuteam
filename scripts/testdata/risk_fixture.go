// Isolated business fixtures; never invokes refund execution or upstream calls.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"net/url"
	"os"
)

func main() {
	must := func(e error) {
		if e != nil {
			panic(e)
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
	db, err := store.Open(ctx, dsn, key)
	must(err)
	defer db.Pool.Close()
	var enabled bool
	must(db.Pool.QueryRow(ctx, "SELECT enabled FROM refund_settings WHERE id").Scan(&enabled))
	if enabled {
		panic("QA queue must remain disabled")
	}
	var actor string
	must(db.Pool.QueryRow(ctx, "SELECT id FROM users WHERE email=$1 AND role='admin'", os.Getenv("BOOTSTRAP_EMAIL")).Scan(&actor))
	for _, table := range []string{"risk_events", "risk_traffic", "risk_conditions", "risk_buckets", "risk_user_controls"} {
		_, err = db.Pool.Exec(ctx, "DELETE FROM "+table)
		must(err)
	}
	old, err := db.RiskSettings(ctx)
	must(err)
	cfg := store.DefaultRiskSettings()
	cfg.Revision = old.Revision
	cfg.RefreshSeconds = 5
	must(db.SaveRiskSettings(ctx, actor, cfg))
	email := "risk-" + store.ID()[:10] + "@example.test"
	password := "Risk-Fixture-2026"
	uid, err := db.AdminCreateUser(ctx, actor, store.UserInput{Email: email, Name: "风控验收客户", Password: password, Role: "member", ServiceFee: 3000})
	must(err)
	must(db.Credit(ctx, email, 30000, "QA fixture"))
	ids, _, err := db.CreateRefunds(ctx, uid, store.ID(), []refundprovider.Account{{Email: "child-" + store.ID()[:10] + "@example.test", Password: "private-child-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	must(err)
	_, err = db.Pool.Exec(ctx, "UPDATE refunds SET created_at=now()-interval '1 hour',message='opaque-protocol-canary export_and_deactivate' WHERE id=$1", ids[0])
	must(err)
	token, err := db.CreateKey(ctx, uid, "风控验收密钥")
	must(err)
	identity, err := db.RiskKeyIdentity(ctx, uid, token)
	must(err)
	for i := 0; i < 4; i++ {
		must(db.RecordRiskRequest(ctx, store.RiskObservation{UserID: uid, Account: email, KeyID: identity.ID, Operation: "submit", Channel: "api", SourceIP: "198.51.100.2", SourceVerified: false, Status: 409, DurationMS: int64(i + 2)}, "duplicate"))
	}
	for i := 0; i < 11; i++ {
		_, err = db.ConsumeRisk(ctx, []store.RiskConsumption{{Rule: "login", Policy: cfg.Rules["login"], Identity: store.RiskIdentity{Type: "login", ID: email, Label: email}}})
		must(err)
	}
	_, err = db.Pool.Exec(ctx, "UPDATE risk_scan_state SET last_scan='epoch'")
	must(err)
	must(db.ScanRisk(ctx))
	raw, err := json.Marshal(map[string]string{"email": email, "user_id": uid, "password": password, "key": token, "key_id": identity.ID})
	must(err)
	must(os.WriteFile(".local/risk-fixture.json", raw, 0600))
}

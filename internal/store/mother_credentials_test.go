package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func motherSessionFixture(t *testing.T) (string, string) {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/profile": map[string]string{"email": "mother@test.invalid"}, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "fixture-team", "chatgpt_plan_type": "team"}, "exp": 2000000000})
	token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	raw, _ := json.Marshal(map[string]any{"accessToken": token, "refresh_token": "fixture-private-rt", "user": map[string]string{"email": "mother@test.invalid"}, "account": map[string]string{"id": "fixture-team", "planType": "team"}})
	return string(raw), token
}

func TestMotherSessionImportPreviewAndSecretPreservation(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	raw, at := motherSessionFixture(t)
	preview, err := store.PreviewRefundMother(raw)
	if err != nil || preview.Email != "mother@test.invalid" || preview.TeamID != "fixture-team" || preview.PlanType != "team" || preview.ExpiresAt == nil || !preview.HasRefresh {
		t.Fatal("metadata parsing failed", err)
	}
	data, _ := json.Marshal(preview)
	if bytes.Contains(data, []byte(at)) || bytes.Contains(data, []byte("fixture-private-rt")) {
		t.Fatal("preview returned credentials")
	}
	proxy, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "dedicated", URL: "proxy.invalid:8080:user:private-proxy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: "session import", Session: raw, ProxyID: proxy, SeatType: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mothers, err := s.RefundMothers(ctx)
	if err != nil || len(mothers) != 1 {
		t.Fatal("mother not saved", err)
	}
	m := mothers[0]
	if m.Email != preview.Email || m.TeamID != preview.TeamID || !m.HasRefresh || m.PlanType != "team" || m.TokenExpiresAt == nil {
		t.Fatal("metadata not persisted")
	}
	var encryptedAT, encryptedRT []byte
	if err = s.Pool.QueryRow(ctx, "SELECT secret,refresh_secret FROM refund_mothers WHERE id=$1", id).Scan(&encryptedAT, &encryptedRT); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encryptedAT, []byte(at)) || bytes.Contains(encryptedRT, []byte("fixture-private-rt")) {
		t.Fatal("credentials stored in plaintext")
	}
	m.Name = "renamed"
	if _, err = s.SaveRefundMother(ctx, a.ID, m); err != nil {
		t.Fatal(err)
	}
	var afterAT, afterRT []byte
	s.Pool.QueryRow(ctx, "SELECT secret,refresh_secret FROM refund_mothers WHERE id=$1", id).Scan(&afterAT, &afterRT)
	if !bytes.Equal(afterAT, encryptedAT) || !bytes.Equal(afterRT, encryptedRT) {
		t.Fatal("blank editing replaced credentials")
	}
	job, err := s.PrepareMotherProbe(ctx, a.ID, store.RefundMother{ID: id})
	if err != nil || job.Mother.AccessToken != at || job.MotherProxy != "http://user:private-proxy@proxy.invalid:8080" || job.ChildProxy != "" {
		t.Fatal("probe does not use saved dedicated proxy", err)
	}
	serialized, _ := json.Marshal(mothers)
	var audits string
	if err = s.Pool.QueryRow(ctx, "SELECT COALESCE(json_agg(a)::text,'[]') FROM audit_logs a").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{at, "fixture-private-rt", "private-proxy"} {
		if strings.Contains(string(serialized)+audits, secret) {
			t.Fatal("settings or audit leaked a credential")
		}
	}
}

func TestMotherCredentialRejectsAmbiguousIdentity(t *testing.T) {
	raw, at := motherSessionFixture(t)
	for _, bad := range []string{"not-a-token", `{"accessToken":"invalid"}`, strings.Replace(raw, "\"id\":\"fixture-team\"", "\"id\":\"different-team\"", 1), `[{"accessToken":"` + at + `"},{"accessToken":"other-token"}]`} {
		if _, err := store.PreviewRefundMother(bad); err == nil {
			t.Fatal("invalid or ambiguous credential accepted")
		}
	}
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	if _, err := s.SaveRefundMother(context.Background(), a.ID, store.RefundMother{Name: "wrong", Session: raw, Email: "other@test.invalid", SeatType: "default"}); err == nil {
		t.Fatal("mismatched email accepted")
	}
}

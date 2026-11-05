package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeToggleRequiresExplicitBoolean(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err = s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(codes[0])).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableCode(ctx, a.ID, id, true); err != nil {
		t.Fatal(err)
	}
	session, err := s.NewSession(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	for _, body := range []string{`{}`, `null`, `{"disabled":null}`, `{"disabled":"false"}`} {
		req, err := http.NewRequest("PATCH", server.URL+"/api/admin/codes/"+id, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "refund_session", Value: session})
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid body returned %d", res.StatusCode)
		}
		var disabled bool
		if err = s.Pool.QueryRow(ctx, "SELECT disabled FROM redeem_codes WHERE id=$1", id).Scan(&disabled); err != nil || !disabled {
			t.Fatal("invalid body enabled code", err)
		}
	}
}

func TestCodeListShowsRedemptionAccounts(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "code-owner@test.invalid", "member")
	v := testutil.Member(t, s, a, "code-history@test.invalid", "member")
	op := testutil.Member(t, s, a, "code-operator@test.invalid", "operator")
	ctx := context.Background()
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 3, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(codes))
	for i, code := range codes {
		if err = s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(code)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Redeem(ctx, u.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	// Preserve every historical recipient rather than duplicating a code row or showing only the last user.
	if _, err = s.Pool.Exec(ctx, "UPDATE redeem_codes SET max_uses=2,used_count=2 WHERE id=$1", ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO redemptions(code_id,user_id,amount,created_at) VALUES($1,$2,1000,'2026-01-01T01:00:00Z'),($1,$3,1000,'2026-01-02T01:00:00Z')`, ids[1], u.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	// A subsequently disabled/deleted account must remain visible in financial history.
	if _, err = s.Pool.Exec(ctx, "UPDATE users SET disabled=true,deleted_at=now() WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	adminSession, err := s.NewSession(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	get := func(page int, session string) (*http.Response, []byte) {
		t.Helper()
		req, e := http.NewRequest("GET", fmt.Sprintf("%s/api/admin/codes?page=%d&size=1", server.URL, page), nil)
		if e != nil {
			t.Fatal(e)
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: session})
		}
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res, body
	}
	items := map[string]store.RedeemCode{}
	for page := 1; page <= 3; page++ {
		res, body := get(page, adminSession)
		var data struct {
			Items []store.RedeemCode `json:"items"`
			Total int                `json:"total"`
		}
		if res.StatusCode != 200 || json.Unmarshal(body, &data) != nil || data.Total != 3 || len(data.Items) != 1 {
			t.Fatalf("invalid page: status=%d body=%s", res.StatusCode, body)
		}
		items[data.Items[0].ID] = data.Items[0]
		for _, code := range codes {
			if strings.Contains(string(body), code) {
				t.Fatal("listing exposed full code")
			}
		}
	}
	if len(items) != 3 {
		t.Fatal("redemption history changed pagination")
	}
	single := items[ids[0]].Redemptions
	if len(single) != 1 || single[0].UserID != u.ID || single[0].Email != u.Email || single[0].CreatedAt.IsZero() {
		t.Fatalf("missing recipient: %+v", single)
	}
	history := items[ids[1]].Redemptions
	if len(history) != 2 || history[0].Email != u.Email || history[1].Email != v.Email || !history[0].CreatedAt.Before(history[1].CreatedAt) {
		t.Fatalf("missing historical recipients: %+v", history)
	}
	if unused := items[ids[2]].Redemptions; unused == nil || len(unused) != 0 {
		t.Fatalf("unused code history: %+v", unused)
	}
	for _, user := range []store.User{v, op} {
		session, e := s.NewSession(ctx, user.ID)
		if e != nil {
			t.Fatal(e)
		}
		res, body := get(1, session)
		if res.StatusCode != 403 || strings.Contains(string(body), u.Email) {
			t.Fatalf("recipient information exposed to %s", user.Role)
		}
	}
	if res, _ := get(1, ""); res.StatusCode != 401 {
		t.Fatalf("anonymous status=%d", res.StatusCode)
	}
}

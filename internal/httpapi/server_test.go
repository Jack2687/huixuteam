package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPPermissionsIsolationAndKeys(t *testing.T) {
	s := testutil.Database(t)
	admin := testutil.Admin(t, s)
	member := testutil.Member(t, s, admin, "member@test.invalid", "member")
	other := testutil.Member(t, s, admin, "other@test.invalid", "member")
	operator := testutil.Member(t, s, admin, "ops@test.invalid", "operator")
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	token := func(u store.User) string {
		v, e := s.NewSession(ctx, u.ID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	ad, mem, ops := token(admin), token(member), token(operator)
	request := func(method, path, body, cookie, bearer, origin string, want int) []byte {
		t.Helper()
		r, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "http-test-idempotency")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, raw)
		}
		return raw
	}
	request("GET", "/api/admin/users", "", "", "", "", 401)
	for _, path := range []string{"users", "codes", "audit", "redemptions", "overview", "refunds"} {
		request("GET", "/api/admin/"+path, "", mem, "", "", 403)
	}
	for _, path := range []string{"users", "codes", "audit", "redemptions"} {
		request("GET", "/api/admin/"+path, "", ops, "", "", 403)
	}
	overview := request("GET", "/api/admin/overview", "", ops, "", "", 200)
	if strings.Contains(string(overview), "balance") {
		t.Fatal("operator received user balances")
	}
	request("GET", "/api/admin/refunds", "", ops, "", "", 200)
	request("POST", "/api/admin/codes", `{"count":1,"amount":1000,"max_uses":1}`, mem, "", "", 403)
	request("POST", "/api/admin/codes", `{"count":1,"amount":1000,"max_uses":1}`, ad, "", "https://untrusted.invalid", 403)
	request("POST", "/api/admin/users/"+admin.ID+"/credit", `{"amount":1000,"note":"self exploit"}`, mem, "", "", 403)
	raw := request("POST", "/api/admin/codes", `{"count":1,"amount":1000,"max_uses":1}`, ad, "", "", 200)
	var codes struct {
		Codes []string `json:"codes"`
	}
	json.Unmarshal(raw, &codes)
	list := request("GET", "/api/admin/codes", "", ad, "", "", 200)
	if strings.Contains(string(list), codes.Codes[0]) {
		t.Fatal("code list leaked full code")
	}
	var listed struct {
		Items []store.RedeemCode `json:"items"`
	}
	if err := json.Unmarshal(list, &listed); err != nil || len(listed.Items) != 1 {
		t.Fatal("invalid code listing", err)
	}
	selection := `{"ids":["` + listed.Items[0].ID + `"]}`
	for _, endpoint := range []string{"export", "delete"} {
		path := "/api/admin/codes/" + endpoint
		request("POST", path, selection, "", "", "", 401)
		request("POST", path, selection, mem, "", "", 403)
		request("POST", path, selection, ops, "", "", 403)
		request("POST", path, selection, ad, "", "https://untrusted.invalid", 403)
		request("POST", path, `{"ids":[]}`, ad, "", "", 400)
	}
	exported := request("POST", "/api/admin/codes/export", selection, ad, "", "", 200)
	if !strings.Contains(string(exported), codes.Codes[0]) {
		t.Fatal("selected export did not return full code")
	}
	request("POST", "/api/redeem", `{"code":"`+codes.Codes[0]+`"}`, mem, "", "", 200)
	request("POST", "/api/redeem", `{"code":"`+codes.Codes[0]+`"}`, mem, "", "", 400)
	request("POST", "/api/admin/users/"+member.ID+"/credit", `{"amount":-100,"note":"correction"}`, mem, "", "", 403)
	for i := 0; i < 2; i++ {
		request("POST", "/api/admin/users/"+member.ID+"/credit", `{"amount":-100,"note":"correction"}`, ad, "", "", 200)
	}
	request("POST", "/api/admin/codes/delete", selection, ad, "", "", 200)
	request("POST", "/api/admin/codes/export", selection, ad, "", "", 400)
	request("PATCH", "/api/admin/codes/"+listed.Items[0].ID, `{"disabled":false}`, ad, "", "", 404)
	if err := s.AdminCredit(ctx, admin.ID, member.ID, "http-funding", 10000, "test"); err != nil {
		t.Fatal(err)
	}
	ids, _, err := s.CreateRefunds(ctx, member.ID, "http-request", []refundprovider.Account{{Email: "target@test.invalid", Password: "private-secret", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/refunds/"+ids[0], "", token(other), "", "", 404)
	detail := request("GET", "/api/refunds/"+ids[0], "", mem, "", "", 200)
	if strings.Contains(string(detail), "private-secret") || strings.Contains(string(detail), "JBSWY3DPEHPK3PXP") {
		t.Fatal("credentials leaked")
	}
	request("GET", "/api/admin/refunds/"+ids[0], "", ops, "", "", 200)
	key, err := s.CreateKey(ctx, member.ID, "http-key")
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/open/v1/requests", "", "", key, "", 200)
	request("GET", "/api/admin/users", "", "", key, "", 401)
	keys, err := s.Keys(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	request("DELETE", "/api/keys/"+keys[0].ID, "", token(other), "", "", 404)
	request("DELETE", "/api/keys/"+keys[0].ID, "", mem, "", "", 200)
	request("GET", "/open/v1/requests", "", "", key, "", 401)
	request("POST", "/api/refunds/"+ids[0]+"/cancel", "", mem, "", "", 200)
	request("POST", "/api/refunds/"+ids[0]+"/cancel", "", mem, "", "", 200)
	request("POST", "/api/admin/users/"+member.ID+"/password", `{"password":"Changed-Password-123"}`, ad, "", "", 200)
	request("GET", "/api/auth/me", "", mem, "", "", 401)
	request("GET", "/api/not-a-route", "", ad, "", "", 404)
	request("GET", "/health/ready", "", "", "", "", 200)
}

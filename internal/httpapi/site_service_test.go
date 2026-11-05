package httpapi_test

import (
	"context"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServicePriceAdminOnlyAndValidation(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "price-member@test.invalid", "member")
	o := testutil.Member(t, s, a, "price-operator@test.invalid", "operator")
	ad, _ := s.NewSession(ctx, a.ID)
	mem, _ := s.NewSession(ctx, u.ID)
	ops, _ := s.NewSession(ctx, o.ID)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	call := func(method, path, body, token, origin string, want int) string {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s: status %d expected %d: %s", path, res.StatusCode, want, raw)
		}
		return string(raw)
	}
	path := "/api/admin/site-settings/service"
	for _, method := range []string{"GET", "PUT"} {
		call(method, path, `{"default_service_fee":1}`, "", "", 401)
		call(method, path, `{"default_service_fee":1}`, mem, "", 403)
		call(method, path, `{"default_service_fee":1}`, ops, "", 403)
	}
	if !strings.Contains(call("GET", path, "", ad, "", 200), `"default_service_fee":10000`) {
		t.Fatal("wrong initial price")
	}
	call("PUT", path, `{"default_service_fee":500}`, ad, "https://untrusted.invalid", 403)
	for _, body := range []string{`{}`, `{"default_service_fee":null}`, `{"default_service_fee":-1}`, `{"default_service_fee":10000001}`, `{"default_service_fee":1.1}`, `{"default_service_fee":"100"}`} {
		call("PUT", path, body, ad, "", 400)
	}
	call("PUT", path, `{"default_service_fee":12345}`, ad, "", 200)
	if !strings.Contains(call("GET", path, "", ad, "", 200), `"default_service_fee":12345`) {
		t.Fatal("price not persisted")
	}
	if strings.Contains(call("GET", "/api/public/site", "", "", "", 200), "default_service_fee") {
		t.Fatal("admin pricing config leaked")
	}
}

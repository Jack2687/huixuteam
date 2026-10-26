package httpapi_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAdminServiceFeeBatchHTTPPermissionsAndRequiredPrice(t *testing.T) {
	db := testutil.Database(t)
	a := testutil.Admin(t, db)
	u := testutil.Member(t, db, a, "price-http@test.invalid", "member")
	ops := testutil.Member(t, db, a, "price-ops@test.invalid", "operator")
	ctx := context.Background()
	token := func(user store.User) string {
		v, err := db.NewSession(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ad, member, operator := token(a), token(u), token(ops)
	key, err := db.CreateKey(ctx, a.ID, "admin key is not a session")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.New(db, false))
	defer srv.Close()
	request := func(body, cookie, bearer, origin string, want int) string {
		t.Helper()
		r, err := http.NewRequest("PATCH", srv.URL+"/api/admin/users/service-fee", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		res, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("got %d want %d: %s", res.StatusCode, want, raw)
		}
		return string(raw)
	}
	body := fmt.Sprintf(`{"ids":[%q,%q],"service_fee":1234}`, u.ID, ops.ID)
	request(body, "", "", "", 401)
	request(body, member, "", "", 403)
	request(body, operator, "", "", 403)
	request(body, "", key, "", 401)
	request(body, ad, "", "https://untrusted.invalid", 403)
	for _, suffix := range []string{"", `,"service_fee":null`, `,"service_fee":-1`, `,"service_fee":10000001`, `,"service_fee":1.5`, `,"service_fee":"1200"`, `,"service_fee":1200,"role":"admin"`} {
		request(fmt.Sprintf(`{"ids":[%q]%s}`, u.ID, suffix), ad, "", "", 400)
	}
	request(`{"ids":[],"service_fee":0}`, ad, "", "", 400)
	request(fmt.Sprintf(`{"ids":[%q,"unknown"],"service_fee":0}`, u.ID), ad, "", "", 400)
	got, _ := db.User(ctx, u.ID)
	if got.ServiceFee != 3000 {
		t.Fatal("invalid request changed price")
	}
	if raw := request(body, ad, "", "", 200); !strings.Contains(raw, `"updated":2`) {
		t.Fatal(raw)
	}
	for _, id := range []string{u.ID, ops.ID} {
		got, err := db.User(ctx, id)
		if err != nil || got.ServiceFee != 1234 {
			t.Fatal(got, err)
		}
	}
	request(fmt.Sprintf(`{"ids":[%q],"service_fee":0}`, u.ID), ad, "", "", 200)
}

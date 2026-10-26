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

func TestAdminUserEditDeletePermissionsAndValidation(t *testing.T) {
	db := testutil.Database(t)
	a := testutil.Admin(t, db)
	u := testutil.Member(t, db, a, "edit-http@test.invalid", "member")
	ops := testutil.Member(t, db, a, "operator-http@test.invalid", "operator")
	ctx := context.Background()
	token := func(user store.User) string {
		v, err := db.NewSession(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ad, mem, operator := token(a), token(u), token(ops)
	srv := httptest.NewServer(httpapi.New(db, false))
	defer srv.Close()
	request := func(method, path, body, cookie, origin string, want int) {
		t.Helper()
		r, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
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
			t.Fatalf("%s got %d want %d: %s", path, res.StatusCode, want, raw)
		}
	}
	path := "/api/admin/users/" + u.ID
	body := `{"email":"changed-http@test.invalid","name":"New name","role":"member","service_fee":3000,"disabled":false}`
	for _, method := range []string{"PATCH", "DELETE"} {
		request(method, path, body, "", "", 401)
		request(method, path, body, mem, "", 403)
		request(method, path, body, operator, "", 403)
		request(method, path, body, ad, "https://untrusted.invalid", 403)
	}
	for _, email := range []string{"", "broken", a.Email} {
		request("PATCH", path, fmt.Sprintf(`{"email":%q,"name":"New name","role":"member","service_fee":3000}`, email), ad, "", 400)
	}
	request("PATCH", path, body, ad, "", 200)
	request("GET", "/api/auth/me", "", mem, "", 401)
	got, err := db.Login(ctx, "changed-http@test.invalid", "TestPassword-12345")
	if err != nil || got.Name != "New name" {
		t.Fatal(got, err)
	}
	request("DELETE", "/api/admin/users/"+a.ID, "", ad, "", 400)
	request("DELETE", path, "", ad, "", 200)
	request("DELETE", path, "", ad, "", 200)
	request("PATCH", path, body, ad, "", 404)
	request("DELETE", "/api/admin/users/not-present", "", ad, "", 404)
}

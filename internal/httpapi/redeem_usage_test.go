package httpapi_test

import (
	"context"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedeemUsagePermissions(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "usage-member@test.invalid", "member")
	srv := httptest.NewServer(httpapi.New(s, false))
	defer srv.Close()
	for _, tc := range []struct {
		user  *store.User
		query string
		want  int
	}{{nil, "", 401}, {&u, "", 403}, {&a, "", 200}, {&a, "?period=hour", 400}, {&a, "?date=2026-02-30", 400}} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/admin/codes/usage"+tc.query, nil)
		if tc.user != nil {
			token, err := s.NewSession(context.Background(), tc.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatal(tc.query, res.StatusCode, tc.want)
		}
	}
}

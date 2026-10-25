package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestAccountReportPermissionsAndValidation(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "member@test.invalid", "member")
	o := testutil.Member(t, s, a, "operator@test.invalid", "operator")
	srv := httptest.NewServer(httpapi.New(s, false))
	defer srv.Close()
	for _, tc := range []struct {
		user   *store.User
		query  string
		status int
	}{
		{nil, "", 401}, {&u, "", 403}, {&o, "", 403}, {&a, "", 200},
		{&a, "?exclude_admin=false", 200}, {&a, "?exclude_admin=oops", 400},
		{&a, "?period=hour", 400}, {&a, "?date=2026-02-30", 400},
		{&a, "?q=" + strings.Repeat("x", 255), 400},
	} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/admin/reports/accounts"+tc.query, nil)
		if tc.user != nil {
			token, err := s.NewSession(context.Background(), tc.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("status %d want %d: %s", resp.StatusCode, tc.status, raw)
		}
		if tc.status == 200 {
			var report store.AccountReport
			if err = json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			want := 2
			if tc.query != "" {
				want = 3
			}
			if report.Total != want || report.Size != 100 {
				t.Fatal("default exclusion/pagination", report)
			}
			for _, secret := range []string{"password", "credentials", "totp", "access_token", "proxy", "export_and_deactivate"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("private field returned", secret)
				}
			}
		}
	}
}

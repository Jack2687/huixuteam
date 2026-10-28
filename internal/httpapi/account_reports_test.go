package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestAccountReportExclusionEndpoints(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "excluded@test.invalid", "member")
	o := testutil.Member(t, s, a, "operator@test.invalid", "operator")
	srv := httptest.NewServer(httpapi.New(s, false))
	defer srv.Close()
	for _, tc := range []struct {
		user   *store.User
		path   string
		status int
	}{
		{nil, "/options", 401}, {&u, "/options", 403}, {&o, "/options", 403}, {&a, "/options", 200},
		{&a, "/options?exclude_admin=bad", 400}, {&a, "/options?q=" + strings.Repeat("x", 255), 400},
		{&a, "?exclude_user_id=invalid", 400}, {&a, "?exclude_user_id=", 400},
		{&a, "?" + strings.Repeat("exclude_user_id="+u.ID+"&", 201), 400},
		{&a, "?exclude_user_id=" + u.ID + "&exclude_user_id=" + o.ID, 200},
		{&a, "/options?q=" + url.QueryEscape("EXCLUDED@"), 200},
	} {
		req, _ := http.NewRequest("GET", srv.URL+"/api/admin/reports/accounts"+tc.path, nil)
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
			t.Fatalf("%s: %d: %s", tc.path, resp.StatusCode, raw)
		}
		if tc.status != 200 {
			continue
		}
		for _, forbidden := range []string{"password", "credentials", "totp", "access_token", "proxy", "export_and_deactivate"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatal("private field exposed", forbidden)
			}
		}
		if strings.HasPrefix(tc.path, "/options") {
			var value store.AccountReportOptions
			if err = json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tc.path, "q=") && (value.Total != 1 || value.Items[0].ID != u.ID) {
				t.Fatal("options search failed", value)
			}
			for _, forbidden := range []string{"balance", "service_fee", "frozen"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatal("selector exposed unnecessary financial field", forbidden)
				}
			}
		} else {
			var report store.AccountReport
			if err = json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if report.Total != 0 || len(report.Items) != 0 || report.Summary != (store.AccountReportMetrics{}) {
				t.Fatal("HTTP multi-selection not applied", report)
			}
		}
	}
}

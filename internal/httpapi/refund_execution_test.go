package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"llm-refund/webui"
)

func TestExecutionHTTPPermissionsAndSecretPrivacy(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "member@test.invalid", "member")
	ops := testutil.Member(t, s, a, "operator@test.invalid", "operator")
	ctx := context.Background()
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	token := func(user store.User) string {
		v, err := s.NewSession(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ad, member, operator := token(a), token(u), token(ops)
	request := func(method, path, body, cookie string, want int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if strings.HasPrefix(cookie, "Bearer ") {
			r.Header.Set("Authorization", cookie)
		} else if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, response.StatusCode, want, raw)
		}
		return raw
	}
	for _, cookie := range []string{member, operator} {
		request("GET", "/api/admin/refund-settings", "", cookie, 403)
		request("PUT", "/api/admin/refund-settings", `{"mother_interval_seconds":30}`, cookie, 403)
		request("POST", "/api/admin/refund-mothers", `{}`, cookie, 403)
		request("POST", "/api/admin/refund-mothers/preview", `{"session":"private"}`, cookie, 403)
		request("POST", "/api/admin/refund-mothers/check", `{}`, cookie, 403)
		request("POST", "/api/admin/refund-proxies", `{}`, cookie, 403)
		request("POST", "/api/admin/refund-proxies/check", `{}`, cookie, 403)
		request("POST", "/api/admin/refunds/fake/resume", `{"action":"resume","note":"test reason"}`, cookie, 403)
	}
	request("GET", "/api/admin/refund-settings", "", "", 401)
	raw := request("POST", "/api/admin/refund-proxies", `{"name":"HTTP fixture","url":"proxy.invalid:8080:user:proxy-secret","enabled":true}`, ad, 200)
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &created)
	request("POST", "/api/admin/refund-mothers", `{"name":"Mother fixture","email":"mother@test.invalid","team_id":"team-fixture","proxy_id":"`+created.ID+`","seat_type":"default","access_token":"private-mother-at","enabled":true}`, ad, 200)
	raw = request("GET", "/api/admin/refund-settings", "", ad, 200)
	for _, secret := range []string{"proxy-secret", "private-mother-at"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("admin GET leaked secret")
		}
	}
	request("PUT", "/api/admin/refund-settings", `{"enabled":true,"max_parallel":2,"max_attempts":2,"retry_seconds":30}`, ad, 400)
	s.SetRefundRuntimeAvailable(true)
	request("PUT", "/api/admin/refund-settings", `{"enabled":true,"max_parallel":2,"max_attempts":2,"retry_seconds":30,"child_proxy_id":"`+created.ID+`"}`, ad, 200)
	request("PUT", "/api/admin/refund-settings", `{"mother_interval_seconds":45}`, ad, 200)
	request("PUT", "/api/admin/refund-settings", `{"retry_seconds":10}`, ad, 200)
	for _, body := range []string{`{"mother_interval_seconds":-1}`, `{"mother_interval_seconds":86401}`, `{"mother_interval_seconds":1.5}`} {
		request("PUT", "/api/admin/refund-settings", body, ad, 400)
	}
	cfg, err := s.RefundSettings(ctx)
	if err != nil || cfg.MotherIntervalSeconds != 45 {
		t.Fatal("partial settings update reset interval", cfg, err)
	}
	s.Credit(ctx, u.Email, 9000, "test funding")
	ids, _, err := s.CreateRefunds(ctx, u.ID, "http-execution-test", []refundprovider.Account{{Email: "customer@test.invalid", Password: "private-child-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimRefund(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if job.ChildProxy != "http://user:proxy-secret@proxy.invalid:8080" {
		t.Fatal("worker did not receive normalized HTTP proxy")
	}
	raw = request("GET", "/api/refunds/"+ids[0], "", member, 200)
	for _, secret := range []string{"mother_id", "private-child-password", "JBSWY3DPEHPK3PXP", "private-mother-at", "proxy-secret", "mother@test.invalid", "team-fixture"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("customer detail leaked execution secret", secret)
		}
	}
	if !strings.Contains(string(raw), `开始处理`) {
		t.Fatal("missing generic progress")
	}
	// Exercise actual private checkpoints and a legacy accidentally-public event.
	for _, method := range []string{"inviting", "invited", "login", "merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted"} {
		if err := s.RefundCheckpoint(ctx, job, store.Checkpoint{Method: method, TeamID: job.Mother.TeamID, UserID: "secret-child-id"}); err != nil {
			t.Fatal(err)
		}
	}
	s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message) VALUES($1,'export_and_deactivate secret legacy row')", job.ID)
	s.Pool.Exec(ctx, "UPDATE refunds SET message='export_and_deactivate raw upstream reply' WHERE id=$1", job.ID)
	for _, who := range []struct{ path, cookie string }{{"/api/refunds/", member}, {"/api/admin/refunds/", ad}, {"/api/admin/refunds/", operator}} {
		body := request("GET", who.path+job.ID, "", who.cookie, 200)
		for _, word := range []string{"export_and_deactivate", "merge_v2", "invited", "accepted_at", "internal_events", "execution", "secret-child-id", "停用", "团队", "登录"} {
			if strings.Contains(string(body), word) {
				t.Fatalf("%s leaked internal progress %s", who.path, word)
			}
		}
	}
	// Raw worker failures, legacy notifications and all read surfaces remain
	// generic even for an administrator, operator or the customer's API key.
	private := "export_and_deactivate merge_v2 private-child-password JBSWY3DPEHPK3PXP private-mother-at proxy-secret secret-child-id"
	if err := s.RefundNeedsAttention(ctx, job, private); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "INSERT INTO notifications(user_id,message) VALUES($1,$2)", u.ID, private); err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateKey(ctx, u.ID, "privacy test")
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []struct{ path, credential string }{
		{"/api/refunds", member}, {"/api/refunds/" + job.ID, member}, {"/api/dashboard", member}, {"/api/billing", member}, {"/api/notifications", member},
		{"/api/admin/refunds", ad}, {"/api/admin/refunds/" + job.ID, ad}, {"/api/admin/overview", ad}, {"/api/admin/audit", ad},
		{"/api/admin/refunds", operator}, {"/api/admin/refunds/" + job.ID, operator},
		{"/open/v1/requests", "Bearer " + key}, {"/open/v1/requests/" + job.ID, "Bearer " + key},
	} {
		body := request("GET", who.path, "", who.credential, 200)
		for _, word := range append(strings.Fields(private), "accepted_at", "child_user_id", "lease_token", "internal_events", "停用", "移出团队") {
			if strings.Contains(string(body), word) {
				t.Fatalf("private data in %s: %s", who.path, word)
			}
		}
	}
	request("POST", "/api/admin/refunds/"+job.ID+"/resume", `{"action":"resume","note":"fixture recovery checked"}`, ad, 200)
	for _, word := range strings.Fields(private) {
		if strings.Contains(string(request("GET", "/api/admin/audit", "", ad, 200)), word) {
			t.Fatal("recovery audit leaked private data")
		}
	}
	// Private frontend assets have server-side authentication, including CSS.
	files, err := fs.Glob(webui.Static, "static/assets/admin-*")
	if err != nil || len(files) < 2 {
		t.Fatal("build protected admin chunks before testing", err)
	}
	for _, file := range files {
		path := "/" + strings.TrimPrefix(file, "static/")
		request("GET", path, "", "", 401)
		request("GET", path, "", member, 403)
		if strings.Contains(file, "admin-execution-") || strings.Contains(file, "admin-timeline-") {
			request("GET", path, "", operator, 403)
		}
		request("GET", path, "", ad, 200)
	}
	for _, path := range []string{"/runtime/refund_runner.py", "/docs/REFUND-INTEGRATION.md", "/.env", "/internal/store/schema.sql", "/.git/config", "/.local/server.err.log", "/api/admin/logs", "/api/admin/refunds/" + job.ID + "/execution", "/api/admin/refunds/" + job.ID + "/events/internal", "/assets/index.js.map", "/assets/%2e%2e/%2e%2e/runtime/refund_runner.py", "/runtime%2fmanager_oauth%2fmerge_v2.py"} {
		for _, cookie := range []string{"", member, ad} {
			request("GET", path, "", cookie, 404)
		}
	}
}

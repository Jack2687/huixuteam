package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestPasswordFailurePublicResultAndSessionRemainUsable(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "member@test.invalid", "member")
	other := testutil.Member(t, s, a, "other@test.invalid", "member")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Credit(ctx, u.Email, 90000, "fixture"))
	p, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "Fixture", URL: "http://private-proxy.invalid:8080", Enabled: true})
	must(err)
	_, err = s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: "Fixture", Email: "mother@test.invalid", TeamID: "private-team", ProxyID: p, SeatType: "default", AccessToken: "private-token", Enabled: true})
	must(err)
	must(s.SaveRefundSettings(ctx, a.ID, store.RefundSettings{ChildProxyID: p, Enabled: true, MaxParallel: 2, LoginAttempts: 1, RetrySeconds: 5}))
	ids, _, err := s.CreateRefunds(ctx, u.ID, "password-http", []refundprovider.Account{{Email: "child@test.invalid", Password: "private-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
	must(err)
	j, err := s.ClaimRefund(ctx)
	must(err)
	if j == nil {
		t.Fatal("no job")
	}
	for _, method := range []string{"inviting", "invited", "login"} {
		must(s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: method}))
	}
	must(s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: "password_failed", Diagnostic: &store.RefundDiagnostic{Code: "password_verification_failed", HTTPStatus: 401, Evidence: &store.RefundEvidence{Stage: "password", Side: "child", Reason: "incorrect_password"}}}))
	must(s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: "cleanup_started"}))
	must(s.RefundCheckpoint(ctx, j, store.Checkpoint{Method: "cleanup_completed"}))
	// Old persisted 401 messages must also be corrected at the read boundary.
	legacy := "密码验证失败（401），请核对 ChatGPT 登录密码。冻结服务费已释放。"
	_, err = s.Pool.Exec(ctx, "UPDATE refunds SET message=$2 WHERE id=$1", j.ID, legacy)
	must(err)
	_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message) VALUES($1,$2)", j.ID, legacy)
	must(err)
	cookie, err := s.NewSession(ctx, u.ID)
	must(err)
	otherCookie, err := s.NewSession(ctx, other.ID)
	must(err)
	key, err := s.CreateKey(ctx, u.ID, "Fixture")
	must(err)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	for _, tc := range []struct {
		path, token string
		bearer      bool
		status      int
		hasCode     bool
	}{
		{"/api/refunds/" + ids[0], cookie, false, 200, true},
		{"/api/refunds", cookie, false, 200, true},
		{"/open/v1/requests/" + ids[0], key, true, 200, true},
		{"/open/v1/requests", key, true, 200, true},
		{"/api/refunds/" + ids[0], otherCookie, false, 404, false},
		{"/api/refunds/" + ids[0], "", false, 401, false},
	} {
		req, _ := http.NewRequest("GET", server.URL+tc.path, nil)
		if tc.bearer {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		} else if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: tc.token})
		}
		res, err := server.Client().Do(req)
		must(err)
		raw, err := io.ReadAll(res.Body)
		res.Body.Close()
		must(err)
		body := string(raw)
		if res.StatusCode != tc.status {
			t.Fatal(tc.path, res.StatusCode, body)
		}
		if tc.hasCode && (!strings.Contains(body, `"http_status":401`) || !strings.Contains(body, `"error_code":"login_verification_failed"`) || !strings.Contains(body, "登录验证未通过")) {
			t.Fatal(tc.path, body)
		}
		for _, forbidden := range []string{"密码验证失败", "请核对 ChatGPT 登录密码", "private-", "terminal_code", "diagnostic", "cleanup", "auth.openai.com", "/invites", "access_token", "JBSWY3DPEHPK3PXP"} {
			if strings.Contains(body, forbidden) {
				t.Fatal("leak", forbidden, tc.path)
			}
		}
	}
}

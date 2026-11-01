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
	"llm-refund/internal/testutil"
)

func TestReleasedLoginReasonsVisibleToOwner(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "login-result@test.invalid", "member")
	other := testutil.Member(t, s, a, "other-result@test.invalid", "member")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Credit(ctx, u.Email, 90000, "fixture"))
	cookie, err := s.NewSession(ctx, u.ID)
	must(err)
	otherCookie, err := s.NewSession(ctx, other.ID)
	must(err)
	key, err := s.CreateKey(ctx, u.ID, "Fixture")
	must(err)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	for _, tc := range []struct{ name, message string }{
		{"verification", "登录需要额外验证码，服务费已释放，可再次尝试。"},
		{"deactivated", "账号已停用，本次未能办理，服务费已释放。"},
	} {
		ids, _, err := s.CreateRefunds(ctx, u.ID, tc.name, []refundprovider.Account{{Email: tc.name + "@test.invalid", Password: "private-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
		must(err)
		must(s.Transition(ctx, u.ID, ids[0], "processing", "开始处理"))
		must(s.Transition(ctx, u.ID, ids[0], "failed", tc.message))
		for _, check := range []struct {
			path, token string
			bearer      bool
			status      int
		}{
			{"/api/refunds/" + ids[0], cookie, false, 200},
			{"/api/refunds", cookie, false, 200},
			{"/api/notifications", cookie, false, 200},
			{"/open/v1/requests/" + ids[0], key, true, 200},
			{"/open/v1/requests", key, true, 200},
			{"/api/refunds/" + ids[0], otherCookie, false, 404},
		} {
			req, _ := http.NewRequest("GET", server.URL+check.path, nil)
			if check.bearer {
				req.Header.Set("Authorization", "Bearer "+check.token)
			} else {
				req.AddCookie(&http.Cookie{Name: "refund_session", Value: check.token})
			}
			resp, err := server.Client().Do(req)
			must(err)
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			must(err)
			if resp.StatusCode != check.status {
				t.Fatal(check.path, resp.StatusCode)
			}
			if check.status == 200 && !strings.Contains(string(raw), tc.message) {
				t.Fatal("reason hidden", check.path, string(raw))
			}
			for _, forbidden := range []string{"private-password", "JBSWY3DPEHPK3PXP", "diagnostic", "account_deactivated", "extra_verification"} {
				if strings.Contains(string(raw), forbidden) {
					t.Fatal("private evidence leaked", forbidden)
				}
			}
		}
	}
}

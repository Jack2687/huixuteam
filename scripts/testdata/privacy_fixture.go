// Local browser display fixtures only. Never starts the upstream worker.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	dsn := os.Getenv("DATABASE_URL")
	uri, err := url.Parse(dsn)
	must(err)
	if uri.Hostname() != "127.0.0.1" || uri.Port() != "18742" || uri.Path != "/llm_refund_qa" {
		panic("isolated QA database required")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("MASTER_KEY"))
	must(err)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn, key)
	must(err)
	defer s.Pool.Close()
	var enabled bool
	must(s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_settings WHERE id").Scan(&enabled))
	if enabled {
		panic("QA worker must remain disabled")
	}
	var actor string
	must(s.Pool.QueryRow(ctx, "SELECT id FROM users WHERE email=$1 AND role='admin'", os.Getenv("BOOTSTRAP_EMAIL")).Scan(&actor))
	email := "privacy-" + store.ID()[:10] + "@example.test"
	password := "Browser-Privacy-2026"
	uid, err := s.AdminCreateUser(ctx, actor, store.UserInput{Email: email, Name: "保密验收", Password: password, Role: "member", ServiceFee: 3000})
	must(err)
	must(s.Credit(ctx, email, 9000, "QA privacy fixture"))
	output := map[string]any{"email": email, "password": password}
	announcements := []string{}
	for _, in := range []store.AnnouncementInput{
		{Title: "登录页公告：服务指引", Content: "提交申请后，可在服务台查看办理动态。", Category: "notice", Published: true, Pinned: true},
		{Title: "登录页公告：上线说明", Content: "公告可以左右切换，长内容可以继续滚动查看。\n<script>window.loginAnnouncementXSS=true</script>", Category: "update", Published: true, Pinned: true},
		{Title: "登录页不可见的验收草稿", Content: "draft-private-canary", Category: "notice", Published: false, Pinned: true},
	} {
		id, err := s.SaveAnnouncement(ctx, actor, "", in)
		must(err)
		announcements = append(announcements, id)
	}
	output["announcements"] = announcements
	canary := "opaque-protocol-canary export_and_deactivate https://auth.openai.com private-child-password"
	for _, status := range []string{"succeeded", "attention"} {
		account := status + "-" + store.ID()[:10] + "@example.test"
		ids, _, err := s.CreateRefunds(ctx, uid, store.Token(), []refundprovider.Account{{Email: account, Password: "private-child-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
		must(err)
		must(s.Transition(ctx, uid, ids[0], "processing", "开始处理"))
		message := "退款服务已完成"
		if status == "attention" {
			message = "申请需要跟进，请联系服务专员。"
		}
		must(s.Transition(ctx, uid, ids[0], status, message))
		_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true),($1,$2,false)", ids[0], canary)
		must(err)
		_, err = s.Pool.Exec(ctx, "UPDATE refunds SET message=$2 WHERE id=$1", ids[0], canary)
		must(err)
		output[status] = map[string]string{"id": ids[0], "email": account}
	}
	_, err = s.Pool.Exec(ctx, "INSERT INTO notifications(user_id,message) VALUES($1,$2)", uid, canary)
	must(err)
	raw, err := json.Marshal(output)
	must(err)
	must(os.WriteFile(".local/privacy-fixture.json", raw, 0600))
}

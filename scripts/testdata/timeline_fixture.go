// Offline display fixtures. Restricted to the separate QA database; never calls a worker.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
)

func main() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	uri, err := url.Parse(os.Getenv("DATABASE_URL"))
	must(err)
	if uri.Hostname() != "127.0.0.1" || uri.Port() != "18742" || uri.Path != "/llm_refund_qa" {
		panic("isolated QA only")
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("MASTER_KEY"))
	must(err)
	ctx := context.Background()
	s, err := store.Open(ctx, uri.String(), key)
	must(err)
	defer s.Pool.Close()
	var enabled bool
	must(s.Pool.QueryRow(ctx, "SELECT enabled FROM refund_settings WHERE id").Scan(&enabled))
	if enabled {
		panic("QA worker must be disabled")
	}
	var actor string
	must(s.Pool.QueryRow(ctx, "SELECT id FROM users WHERE email=$1 AND role='admin'", os.Getenv("BOOTSTRAP_EMAIL")).Scan(&actor))
	email := "timeline-" + store.ID()[:10] + "@example.test"
	password := "Timeline-QA-2026"
	uid, err := s.AdminCreateUser(ctx, actor, store.UserInput{Email: email, Name: "流程验收", Password: password, Role: "member", ServiceFee: 3000})
	must(err)
	must(s.Credit(ctx, email, 90000, "offline timeline fixture"))
	output := map[string]any{"email": email, "password": password}
	for _, kind := range []string{"succeeded", "attention", "running", "queued", "legacy", "failed"} {
		account := kind + "-" + store.ID()[:8] + "@example.test"
		ids, _, err := s.CreateRefunds(ctx, uid, store.Token(), []refundprovider.Account{{Email: account, Password: "private-child-password", TOTPSecret: "JBSWY3DPEHPK3PXP"}})
		must(err)
		id := ids[0]
		base := time.Now().UTC().Add(-3 * time.Minute)
		_, err = s.Pool.Exec(ctx, "UPDATE refunds SET created_at=$2 WHERE id=$1", id, base)
		must(err)
		if kind != "queued" {
			must(s.Transition(ctx, uid, id, "processing", "开始处理"))
			_, err = s.Pool.Exec(ctx, "UPDATE refund_events SET created_at=$2 WHERE refund_id=$1 AND message='开始处理'", id, base.Add(2*time.Second))
			must(err)
			if kind != "legacy" {
				messages := []string{"正在发送团队邀请。", "团队邀请已确认，准备登录账号。", "正在使用密码和 2FA 登录受邀团队。", "正在使用密码和 2FA 登录受邀团队。", "已确认团队会话，准备提交导出并停用个人空间。", "正在提交导出并停用个人空间。"}
				if kind == "succeeded" {
					messages = append(messages, "导出停用已受理，准备移出团队。", "正在移出本次账号。")
				}
				for i, m := range messages {
					_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal,created_at) VALUES($1,$2,true,$3)", id, m, base.Add(time.Duration(5+i*15)*time.Second))
					must(err)
				}
			}
			state, message := kind, "申请需要跟进，请联系服务专员。"
			if kind == "legacy" || kind == "succeeded" {
				state, message = "succeeded", "退款服务已完成"
			}
			if kind == "failed" {
				message = "本次服务未完成，冻结服务费已释放。"
			}
			if kind != "running" {
				must(s.Transition(ctx, uid, id, state, message))
			}
		}
		_, err = s.Pool.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true),($1,$2,false)", id, "opaque-protocol-canary https://chatgpt.com/backend-api/accounts/export_and_deactivate private-child-password")
		must(err)
		output[kind] = map[string]string{"id": id, "email": account}
	}
	raw, err := json.Marshal(output)
	must(err)
	must(os.WriteFile(".local/timeline-fixture.json", raw, 0600))
}
